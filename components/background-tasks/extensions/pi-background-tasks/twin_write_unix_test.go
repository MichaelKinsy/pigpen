//go:build !windows

package pi_background_tasks_test

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	bg "github.com/MichaelKinsy/pigpen/pi-background-tasks"
)

// Twins of write-path.test.ts. The original's fixture fakes the child process and the timers; the port's child is a real
// `cat` on a fifo, its timers and log appends are the Rig's.

func TestTwinWritePath(t *testing.T) {
	tw(t, "write-path", "streamed output chunks defer log, widget and state writes to their windows", func(t *testing.T) {
		for _, row := range []struct {
			name   string
			chunks int
		}{{"fifty chunks with output wakes on", 50}, {"one chunk arms each window once", 1}} {
			t.Run(row.name, func(t *testing.T) {
				r := rigHost(t, rigOptions{})
				task := r.spawnFifo(t, map[string]any{"notifyOnOutput": true, "notifyOnExit": false})
				// The spawn-time identity read arms the persist window before any chunk, and every chunk's persist request joins it.
				waitFor(t, "the identity read", func() bool { r.rig.Settle(); return r.rig.TimerSets()["timeout:1000"] == 1 })
				before := r.entries()
				chunk := "fixture output line\n"
				for i := 0; i < row.chunks; i++ {
					task.write(t, chunk)
					i := i
					waitFor(t, "the settle window of the chunk", func() bool { return r.rig.TimerSets()["timeout:2000"] == i+1 })
				}
				r.rig.Settle()
				eq(t, r.entries()-before, 0, "no state entry during the chunks")
				eq(t, fileSize(task.logs), 0, "no log bytes during the chunks")
				eq(t, r.rig.TimerSets(), map[string]int{"interval:30000": 1, "timeout:2000": row.chunks, "timeout:250": 1, "timeout:200": 1, "timeout:1000": 1}, "timers set")
				if r.rig.ActiveTimeouts(1000) == 1 {
					r.rig.FireTimeout(t, 1000)
				}
				eq(t, r.entries()-before, 1, "the persist window writes one entry")
				if r.rig.ActiveTimeouts(250) == 1 {
					r.rig.FireTimeout(t, 250)
				}
				r.rig.DrainLogs()
				ident, _ := r.persisted("bg-1")["procIdent"].(map[string]any)
				pid, _ := r.persisted("bg-1")["pid"].(float64)
				if ident == nil || ident["pid"] != pid {
					t.Errorf("procIdent: %v", ident)
				}
				eq(t, fileSize(task.logs), row.chunks*len(chunk), "log bytes after the flush")
				task.exit()
			})
		}
	})

	tw(t, "write-path", "a long snapshot history probes each restored running task once", func(t *testing.T) {
		for _, row := range []struct {
			name    string
			entries int
		}{{"one hundred entries of the same tasks", 100}, {"a single entry", 1}} {
			t.Run(row.name, func(t *testing.T) {
				var identReads, unitReads []string
				var mu = new(chan struct{})
				_ = mu
				ident := map[string]any{"pid": 4242, "startToken": "12345", "comm": "fixture-child"}
				unit := "kendex-pi-bg-bg-2-1700000000000.service"
				snap := func(id string, f map[string]any) map[string]any {
					m := map[string]any{"command": "fixture " + id, "cwd": t.TempDir(), "exitCode": nil, "exitNotified": true, "expiresAt": nil, "id": id,
						"lastOutputAt": nil, "logFile": t.TempDir() + "/" + id + ".log", "notifyOnExit": false, "notifyOnOutput": false, "outputBytes": 0,
						"pid": 0, "sessionId": "write-path-private-session", "startedAt": 1700000000000, "status": "completed", "title": id, "updatedAt": 1700000000000}
					for k, v := range f {
						m[k] = v
					}
					return m
				}
				var branch []map[string]any
				for i := 0; i < row.entries; i++ {
					at := 1700000000000 + i
					branch = append(branch, map[string]any{"type": "custom", "customType": "kendex-background-tasks:state", "data": map[string]any{"version": 1, "updatedAt": at, "tasks": []any{
						snap("bg-1", map[string]any{"status": "running", "pid": 4242, "procIdent": ident, "updatedAt": at}),
						snap("bg-2", map[string]any{"status": "running", "pid": 4243, "resourceControl": map[string]any{"mode": "systemd-run", "requestedMode": "auto", "unitName": unit}, "updatedAt": at}),
						snap("bg-3", map[string]any{"exitCode": 0}),
					}}})
				}
				var lock = make(chan struct{}, 1)
				lock <- struct{}{}
				bg.ProbeHooks(t, func(pid int) (string, string, bool) {
					<-lock
					identReads = append(identReads, jsonOf(pid))
					lock <- struct{}{}
					return "12345", "fixture-child", true
				}, func(name string) bool {
					<-lock
					unitReads = append(unitReads, name)
					lock <- struct{}{}
					return true
				})
				r := rigHost(t, rigOptions{branch: branch, session: "write-path-private-session"})
				<-lock
				// Restore probes the final task set once; the orphan watcher's first pass waits one poll interval. The
				// systemctl availability probe of the original's unit check lives inside the unit probe (see TestTwinSystemdUnitProbe).
				eq(t, identReads, []string{"4242"}, "identity reads")
				eq(t, unitReads, []string{unit}, "unit probes")
				lock <- struct{}{}
				states := map[string]string{}
				for _, id := range []string{"bg-1", "bg-2", "bg-3"} {
					raw, _ := r.Tool("bg_task", map[string]any{"action": "log", "id": id})
					states[id], _ = text(t, raw).Details["task"].(map[string]any)["status"].(string)
				}
				eq(t, states, map[string]string{"bg-1": "running", "bg-2": "running", "bg-3": "completed"}, "states")
			})
		}
	})

	tw(t, "write-path", "a task cleared before its spawn-time identity read resolves keeps no identity", func(t *testing.T) {
		release, stats := bg.HoldIdentityReads(t)
		r := rigHost(t, rigOptions{})
		_, failure := r.Tool("bg_task", map[string]any{"action": "spawn", "command": "true", "notifyOnExit": false})
		eq(t, failure, "", "spawn")
		waitFor(t, "the held read", func() bool { _, n := stats(); return n == 1 })
		r.waitStatus(t, "bg-1", "completed")
		r.Tool("bg_task", map[string]any{"action": "clear"})
		_, n := stats()
		eq(t, n, 1, "held reads")
		release()
		r.rig.Settle()
		time.Sleep(50 * time.Millisecond)
		r.rig.Settle()
		_, live := r.rig.Task("bg-1")
		eq(t, live, false, "the task stays forgotten")
		eq(t, r.rig.ActiveTimeouts(1000), 0, "no persist armed")
	})

	tw(t, "write-path", "restore runs at most PROBE_CONCURRENCY identity reads at once", func(t *testing.T) {
		running := 4 + 2
		release, stats := bg.HoldIdentityReads(t)
		ident := map[string]any{"pid": 4242, "startToken": "12345", "comm": "fixture-child"}
		var tasks []any
		for i := 1; i <= running; i++ {
			tasks = append(tasks, map[string]any{"command": "fixture", "cwd": t.TempDir(), "exitCode": nil, "exitNotified": true, "expiresAt": nil, "id": "bg-" + jsonOf(i),
				"lastOutputAt": nil, "logFile": t.TempDir() + "/x.log", "notifyOnExit": false, "notifyOnOutput": false, "outputBytes": 0, "pid": 4242,
				"sessionId": "write-path-private-session", "startedAt": 1700000000000, "status": "running", "title": "t", "updatedAt": 1700000000000, "procIdent": ident})
		}
		branch := []map[string]any{{"type": "custom", "customType": "kendex-background-tasks:state", "data": map[string]any{"version": 1, "updatedAt": 1700000000000, "tasks": tasks}}}
		started := make(chan struct{})
		var r *rigged
		go func() {
			defer close(started)
			r = rigHost(t, rigOptions{branch: branch, session: "write-path-private-session"})
		}()
		waitFor(t, "the first reads", func() bool { _, n := stats(); return n >= 4 })
		time.Sleep(100 * time.Millisecond)
		maxIn, n := stats()
		eq(t, maxIn, 4, "reads in flight while held")
		eq(t, n, 4, "reads started while held")
		release()
		<-started
		maxIn, n = stats()
		eq(t, maxIn, 4, "highest number of reads at once")
		eq(t, n, running, "reads in all")
		_ = r
	})

	tw(t, "write-path", "a task's exit wake is sent once its log holds the chunk written before close", func(t *testing.T) {
		r := rigHost(t, rigOptions{})
		task := r.spawnFifo(t, map[string]any{"notifyOnExit": true})
		r.rig.SetWakeLog(task.logs)
		task.write(t, "final line\n")
		waitFor(t, "the output to arrive", func() bool { return r.rig.TimerSets()["timeout:250"] == 1 })
		task.exit()
		waitFor(t, "the exit wake", func() bool { return len(r.wakeLogs()) > 0 })
		eq(t, r.wakeLogs(), []string{"final line\n"}, "log at the wake")
	})

	tw(t, "write-path", "a task that outruns its log writes pauses its output until the write in flight ends, and loses none", func(t *testing.T) {
		r := rigHost(t, rigOptions{})
		r.rig.HoldAppends()
		task := r.spawnFifo(t, map[string]any{"notifyOnExit": false, "wrap": "cat '{fifo}'"})
		chunk := strings.Repeat("z", bg.LogWriteNowBytes-1) + "\n"
		chunks := 2 + bg.LogMaxPendingBytes/bg.LogWriteNowBytes
		done := make(chan struct{})
		go func() {
			defer close(done)
			for i := 0; i < chunks; i++ {
				if _, err := task.w.WriteString(chunk); err != nil {
					return
				}
			}
		}()
		waitFor(t, "the pause", func() bool { return r.rig.Paused("bg-1") })
		eq(t, r.rig.HeldAppends(), 1, "held appends")
		r.rig.ReleaseAppends()
		waitFor(t, "output to resume", func() bool { return !r.rig.Paused("bg-1") || fileSize(task.logs) == chunks*len(chunk) })
		waitFor(t, "every byte in the log", func() bool {
			r.rig.ReleaseAppends()
			r.rig.DrainLogs()
			return fileSize(task.logs) == chunks*len(chunk)
		})
		<-done
		eq(t, r.rig.Paused("bg-1"), false, "output resumed")
		task.exit()
	})

	tw(t, "write-path", "a task that exits while its last log write is in flight shows closed at once, takes no stop, timeout or shutdown signal and ends completed", func(t *testing.T) {
		for _, row := range []string{"stop and timeout", "shutdown", "clear"} {
			t.Run(row, func(t *testing.T) {
				r := rigHost(t, rigOptions{})
				signals := bg.CountSignals(t, "pass")
				r.rig.HoldAppends()
				task := r.spawnFifo(t, map[string]any{"notifyOnExit": true, "timeoutSeconds": 60})
				r.rig.SetWakeLog(task.logs)
				task.write(t, "final line\n")
				waitFor(t, "the output to arrive", func() bool { return r.rig.TimerSets()["timeout:250"] == 1 })
				task.exit()
				r.waitStatus(t, "bg-1", "completed")
				r.rig.Settle()
				eq(t, r.rig.HeldAppends(), 1, "held appends")
				completed := map[string]any{"status": "completed", "reason": "self-exit", "exitCode": float64(0)}
				outcome := func() map[string]any {
					p := r.persisted("bg-1")
					if p == nil {
						return map[string]any{}
					}
					return map[string]any{"status": p["status"], "reason": p["terminationReason"], "exitCode": p["exitCode"]}
				}
				switch row {
				case "stop and timeout":
					raw, _ := r.Tool("bg_task", map[string]any{"action": "stop", "id": "bg-1"})
					eq(t, text(t, raw).text(), "bg-1 is already completed (exit 0).", "stop message")
					eq(t, r.rig.ActiveTimeouts(60000), 0, "timeout armed")
					eq(t, signals(), []string{}, "signals")
					r.rig.ReleaseAppends()
					waitFor(t, "the exit wake", func() bool { return len(r.wakeLogs()) > 0 })
					eq(t, outcome(), completed, "outcome")
					eq(t, r.wakeLogs(), []string{"final line\n"}, "log at the wake")
				case "shutdown":
					done := make(chan struct{})
					go func() { defer close(done); r.Fire("session_shutdown", map[string]any{"reason": "quit"}) }()
					time.Sleep(100 * time.Millisecond)
					eq(t, signals(), []string{}, "signals")
					r.rig.ReleaseAppends()
					<-done
					eq(t, outcome(), completed, "outcome")
					eq(t, r.wakeLogs(), []string{}, "no exit wake at shutdown")
				case "clear":
					r.Tool("bg_task", map[string]any{"action": "clear"})
					eq(t, signals(), []string{}, "signals")
					r.rig.ReleaseAppends()
					waitFor(t, "the log to be deleted", func() bool { return fileSize(task.logs) == -1 })
					time.Sleep(50 * time.Millisecond)
					eq(t, r.wakeLogs(), []string{}, "no wake after clear")
					eq(t, outcome(), map[string]any{}, "outcome")
					if _, err := os.Stat(task.logs); err == nil {
						t.Errorf("the held write created the log again")
					}
				}
				// The widget counts at close (running 1/finished 0 before, 0/1 at close) are drawn by slice 3's widget; its twin of this row adds them.
			})
		}
	})

	tw(t, "write-path", "the finished-task bound keeps a task whose exit wake waits for its log flush", func(t *testing.T) {
		r := rigHost(t, rigOptions{})
		r.rig.HoldAppends()
		task := r.spawnFifo(t, map[string]any{"notifyOnExit": true})
		r.rig.SetWakeLog(task.logs)
		task.write(t, "held line\n")
		waitFor(t, "the output to arrive", func() bool { return r.rig.TimerSets()["timeout:250"] == 1 })
		task.exit()
		r.waitStatus(t, "bg-1", "completed")
		// Each quiet task finishes with no output, so its exit settles at once and runs the bound, while bg-1 is the oldest.
		for i := 1; i <= bg.MaxFinishedTasks; i++ {
			_, failure := r.Tool("bg_task", map[string]any{"action": "spawn", "command": "true", "notifyOnExit": false})
			eq(t, failure, "", "spawn")
			r.waitStatus(t, "bg-"+jsonOf(i+1), "completed")
		}
		eq(t, r.rig.HeldAppends(), 1, "held appends")
		r.rig.ReleaseAppends()
		waitFor(t, "the exit wake", func() bool { return len(r.wakeLogs()) > 0 })
		eq(t, r.wakeLogs(), []string{"held line\n"}, "log at the wake")
	})

	tw(t, "write-path", "a task stopped while its log write never settles resumes output at the stall deadline, finalizes and sends its exit wake", func(t *testing.T) {
		r := rigHost(t, rigOptions{})
		r.rig.HoldAppends()
		// The shell ignores SIGTERM and passes that on to cat, so the child keeps reading while the stop is pending.
		task := r.spawnFifo(t, map[string]any{"notifyOnExit": true, "wrap": "trap '' TERM; cat '{fifo}'"})
		r.rig.SetWakeLog(task.logs)
		chunk := strings.Repeat("z", bg.LogWriteNowBytes-1) + "\n"
		chunks := 2 + bg.LogMaxPendingBytes/bg.LogWriteNowBytes
		go func() {
			for i := 0; i < chunks; i++ {
				if _, err := task.w.WriteString(chunk); err != nil {
					return
				}
			}
		}()
		waitFor(t, "the pause", func() bool { return r.rig.Paused("bg-1") })
		eq(t, r.rig.HeldAppends(), 1, "held appends while held")
		raw, _ := r.Tool("bg_task", map[string]any{"action": "stop", "id": "bg-1"})
		eq(t, strings.Split(text(t, raw).text(), " ")[0], "Stopping", "stop message")
		r.rig.FireTimeout(t, bg.LogWriteStallMs)
		waitFor(t, "output to resume", func() bool { return !r.rig.Paused("bg-1") })
		eq(t, r.rig.HeldAppends(), 1, "held appends after the stall")
		waitFor(t, "every chunk read", func() bool {
			task, _ := r.rig.Task("bg-1")
			return task["outputBytes"] == float64(chunks*len(chunk))
		})
		// The child closes by signal, as the original's close(null).
		r.rig.FireTimeout(t, 5000)
		waitFor(t, "the exit wake", func() bool { return len(r.wakeLogs()) > 0 })
		p := r.persisted("bg-1")
		eq(t, []any{p["status"], p["terminationReason"], p["exitCode"]}, []any{"stopped", "extension-stop", nil}, "outcome")
		eq(t, []int{len(r.wakeLogs()[0])}, []int{0}, "log length at the wake")
		// Once the write settles, a marker counts the chunk past the cap after the text kept before it.
		waitFor(t, "the log to settle", func() bool {
			r.rig.ReleaseAppends()
			r.rig.DrainLogs()
			return fileSize(task.logs) > chunks*len(chunk)-len(chunk)
		})
		b, _ := os.ReadFile(task.logs)
		kept := strings.Repeat(chunk, chunks-1)
		// The port's close is the SIGKILL escalation, which logs its own line past the cap, so the marker counts the chunk
		// and that line (the original's close(null) logs nothing more).
		rest := string(b[min(len(b), len(kept)):])
		var dropped int
		if !strings.HasPrefix(string(b), kept) {
			t.Fatalf("the log does not begin with the kept text: %d bytes", len(b))
		}
		if _, err := fmt.Sscanf(rest, "\n[log dropped %d bytes: log write stalled]\n", &dropped); err != nil || dropped < len(chunk) || dropped > len(chunk)+100 {
			t.Errorf("marker after the kept text: %q (%v)", rest, err)
		}
		eq(t, rest, bg.StalledLogMarker(dropped), "the marker")
	})
}
