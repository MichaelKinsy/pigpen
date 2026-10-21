//go:build !windows

package pi_background_tasks_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	bg "github.com/MichaelKinsy/pigpen/pi-background-tasks"
)

// Twins of the registered-extension tests the original runs with a faked child process (spawn-extension.ts): the port runs real
// children and drives the windows through the Rig.

func (r *rigged) taskState(id string) (status, reason string, exitCode any, exitNotified any) {
	task, ok := r.rig.Task(id)
	if !ok {
		task = r.persisted(id)
	}
	if task == nil {
		return "", "", nil, nil
	}
	status, _ = task["status"].(string)
	reason, _ = task["terminationReason"].(string)
	return status, reason, task["exitCode"], task["exitNotified"]
}

func (r *rigged) pidOf(t *testing.T, id string) int {
	task, ok := r.rig.Task(id)
	if !ok {
		t.Fatalf("no task %s", id)
	}
	pid, _ := task["pid"].(float64)
	return int(pid)
}

func TestTwinSpawnHardening(t *testing.T) {
	tw(t, "spawn-hardening", "registered extension spawn hardening rows", func(t *testing.T) {
		t.Run("POSIX spawn requests a detached process group", func(t *testing.T) {
			r := rigHost(t, rigOptions{})
			task := r.spawnFifo(t, map[string]any{"notifyOnExit": false})
			details, _ := task.result.Details["task"].(map[string]any)
			pid := int(details["pid"].(float64))
			// Own session and process group: a signal to the group reaches the task and everything it started.
			pgid, _ := syscall.Getpgid(pid)
			eq(t, pgid, pid, "the child leads its own process group")
			eq(t, []any{task.result.Details["action"], details["id"]}, []any{"spawn", "bg-1"}, "result")
			eq(t, details["cwd"], r.opts.Cwd, "cwd is the session's")
			status, reason, code, notified := r.taskState("bg-1")
			eq(t, []any{status, reason, code, notified}, []any{"running", "", nil, false}, "before")
			task.exit()
			r.waitStatus(t, "bg-1", "completed")
			status, reason, code, notified = r.taskState("bg-1")
			eq(t, []any{status, reason, code, notified}, []any{"completed", "self-exit", float64(0), false}, "final")
			eq(t, r.rig.ActiveTimeouts(60000), 0, "no timers left for the task")
			// stdio is ["ignore", "pipe", "pipe"]: a child that reads its stdin sees the end of file at once.
			_, failure := r.Tool("bg_task", map[string]any{"action": "spawn", "command": "cat; printf '%s|%s' \"$PWD\" \"${PI_CODING_AGENT_DIR-unset}\"", "notifyOnExit": false})
			eq(t, failure, "", "spawn")
			r.waitStatus(t, "bg-2", "completed")
			r.rig.DrainLogs()
			t2, _ := r.rig.Task("bg-2")
			b, _ := os.ReadFile(t2["logFile"].(string))
			agentDir, ok := os.LookupEnv("PI_CODING_AGENT_DIR")
			if !ok {
				agentDir = "unset"
			}
			eq(t, string(b), r.opts.Cwd+"|"+agentDir, "the child's directory and agent directory")
		})
		// The Windows row (attached spawn, taskkill /T /F) has no Windows host here; proc_windows.go is vetted for GOOS=windows and PORT.md records the reasoning.
	})
}

func TestTwinStopContent(t *testing.T) {
	tw(t, "stop-content-e2e", "registered stop content rows", func(t *testing.T) {
		for _, row := range []struct {
			name       string
			caller     string
			signalGone bool
			bomb       string
			status     string
		}{
			{"tool pending stop retains bounded command and compact details", "tool", false, "X", "running"},
			{"tool gone process returns bounded finalized stop content", "tool", true, "X", "stopped"},
			{"slash pending stop notifies with the bounded command", "slash", false, "Y", "running"},
			{"slash gone process notifies with bounded finalized content", "slash", true, "Y", "stopped"},
		} {
			t.Run(row.name, func(t *testing.T) {
				capn := bg.WakeManifestFieldMaxChars
				r := rigHost(t, rigOptions{})
				mode := "swallow"
				if row.signalGone {
					mode = "gone"
				}
				bg.CountSignals(t, mode)
				prefix := "sleep 10 # "
				command := prefix + strings.Repeat(row.bomb, 100_000)
				expected := prefix + strings.Repeat(row.bomb, capn-len(prefix)-1) + "…"
				_, failure := r.Tool("bg_task", map[string]any{"action": "spawn", "command": command, "notifyOnExit": false})
				eq(t, failure, "", "spawn")
				pid := r.pidOf(t, "bg-1")
				t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
				bounded := func(s string) bool {
					return utf8.RuneCountInString(s) < capn+128 && strings.Contains(s, expected) && strings.Contains(s, "bg-1") && !strings.Contains(s, strings.Repeat(row.bomb, capn+1))
				}
				before := len(r.CallsTo("ui.notify"))
				if row.caller == "tool" {
					raw, failure := r.Tool("bg_task", map[string]any{"action": "stop", "id": "bg-1"})
					eq(t, failure, "", "stop")
					out := text(t, raw)
					eq(t, bounded(out.text()), true, "content is bounded and keeps the command and the task")
					task, _ := out.Details["task"].(map[string]any)
					cmd, _ := task["command"].(string)
					eq(t, []any{out.Details["action"], task["id"], cmd == expected, utf8.RuneCountInString(cmd) <= capn}, []any{"stop", "bg-1", true, true}, "details")
					eq(t, len(r.CallsTo("ui.notify")), before, "no notification")
				} else {
					eq(t, r.Command("bg:stop", "bg-1"), "", "/bg:stop")
					notes := r.CallsTo("ui.notify")[before:]
					eq(t, len(notes), 1, "one notification")
					eq(t, []any{notes[0].Args["level"], bounded(notes[0].Args["message"].(string))}, []any{"info", true}, "notification")
				}
				status, _, _, _ := r.taskState("bg-1")
				eq(t, status, row.status, "state")
			})
		}
	})
}

func TestTwinWakeDefaults(t *testing.T) {
	tw(t, "wake-defaults", "unset wake settings run on the shipped defaults", func(t *testing.T) {
		r := rigHost(t, rigOptions{})
		task := r.spawnFifo(t, map[string]any{"notifyOnOutput": true, "notifyOnExit": false})
		// Each burst settles before the next, so each can wake the agent once.
		for i := 0; i < 12; i++ {
			task.write(t, fmt.Sprintf("burst %d\n", i))
			i := i
			waitFor(t, "the settle window", func() bool { return r.rig.TimerSets()["timeout:2000"] == i+1 })
			r.rig.FireTimeout(t, 2000)
			waitFor(t, "the wake decision", func() bool {
				r.rig.Settle()
				task, _ := r.rig.Task("bg-1")
				p, _ := task["pendingWakes"].([]any)
				return len(p) == 0
			})
		}
		r.rig.Settle()
		count := map[string]int{}
		for _, m := range r.messages() {
			d, _ := m["details"].(map[string]any)
			count[fmt.Sprint(d["eventType"])]++
		}
		eq(t, []int{count["output"], count["output-budget-exhausted"]}, []int{10, 1}, "output wakes and budget notices")
		task.exit()
	})
}

func TestTwinResourceStop(t *testing.T) {
	unit := "kendex-pi-bg-bg-1-1700000000000.service"
	set := func(kind string, ms int) string { return fmt.Sprintf("set %s:%d", kind, ms) }
	clr := func(kind string, ms int) string { return fmt.Sprintf("clear %s:%d", kind, ms) }
	interval, persist, timeout, flush, stall := [2]any{"interval", 30000}, [2]any{"timeout", 1000}, [2]any{"timeout", 5000}, [2]any{"timeout", 250}, [2]any{"timeout", bg.LogWriteStallMs}
	S := func(p [2]any) string { return set(p[0].(string), p[1].(int)) }
	C := func(p [2]any) string { return clr(p[0].(string), p[1].(int)) }
	events := map[string][]string{
		"none":          {S(interval), S(persist), C(persist), C(interval)},
		"escalation":    {S(interval), S(persist), C(persist), S(timeout), S(flush), C(timeout), S(stall), C(flush), C(stall), C(interval)},
		"stop":          {S(interval), S(persist), C(persist), S(flush), C(flush), S(stall), C(stall), C(interval), S(flush), C(flush), S(stall), C(stall)},
		"shutdown":      {S(interval), S(persist), C(interval), S(flush), C(persist), C(flush), S(stall), C(stall)},
		"shutdownQuiet": {S(interval), S(persist), C(interval), C(persist)},
	}
	type row struct {
		name                       string
		resource                   bool
		caller                     string
		stopFails, killFails, gone bool
		afterStatus, reason, final string
		escalate                   bool
		failureLogValues           int
		logFlush                   string
	}
	rows := []row{
		{"tool unit stop succeeds without wrapper signals", true, "tool", false, false, false, "running", "extension-stop", "stopped", true, 0, "escalation"},
		{"tool unit stop failure returns an error without fallback signals", true, "tool", true, true, false, "running", "", "running", false, 1, "stop"},
		{"tool without resource control signals the owned task", false, "tool", false, false, false, "running", "extension-stop", "stopped", true, 0, "escalation"},
		{"tool with a gone process finalizes without an escalation timer", false, "tool", false, false, true, "stopped", "extension-stop", "stopped", false, 0, "none"},
		{"shutdown unit stop succeeds without wrapper signals", true, "shutdown", false, false, false, "stopped", "session-shutdown", "stopped", false, 0, "shutdownQuiet"},
		{"shutdown failed unit stops preserve running state without fallback", true, "shutdown", true, true, false, "running", "", "running", false, 3, "shutdown"},
		{"shutdown successful TERM still stops after failed KILL", true, "shutdown", false, true, false, "stopped", "session-shutdown", "stopped", false, 1, "shutdown"},
		{"shutdown successful KILL stops after failed TERM", true, "shutdown", true, false, false, "stopped", "session-shutdown", "stopped", false, 1, "shutdown"},
		{"shutdown without resource control signals the owned task", false, "shutdown", false, false, false, "stopped", "session-shutdown", "stopped", false, 0, "shutdownQuiet"},
		{"shutdown with a gone process refuses stopped state", false, "shutdown", false, false, true, "running", "", "running", false, 0, "shutdown"},
	}
	tw(t, "resource-stop-integration", "registered resource stop and shutdown rows", func(t *testing.T) {
		for _, w := range rows {
			t.Run(w.name, func(t *testing.T) {
				r := rigHost(t, rigOptions{})
				mode := "swallow"
				if w.gone {
					mode = "gone"
				}
				signals := bg.CountSignals(t, mode)
				systemctl := bg.HookSystemctl(t, func(args []string) bool {
					joined := strings.Join(args, " ")
					return (strings.Contains(joined, " stop ") && w.stopFails) || (strings.Contains(joined, " kill ") && w.killFails)
				})
				_, failure := r.Tool("bg_task", map[string]any{"action": "spawn", "command": "sleep 60", "notifyOnExit": false})
				eq(t, failure, "", "spawn")
				pid := r.pidOf(t, "bg-1")
				t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
				if w.resource {
					r.rig.SetResourceControl("bg-1", unit)
				}
				waitFor(t, "the identity read", func() bool { r.rig.Settle(); return r.rig.TimerSets()["timeout:1000"] == 1 })
				status, reason, code, notified := r.taskState("bg-1")
				eq(t, []any{status, reason, code, notified}, []any{"running", "", nil, false}, "before")
				term := []string{"systemctl --user stop --no-block " + unit}
				kill := []string{"systemctl --user kill --signal=SIGKILL " + unit}
				shutdown := w.caller == "shutdown"
				var outcome string
				if shutdown {
					r.Fire("session_shutdown", map[string]any{"reason": "quit"})
				} else {
					raw, failure := r.Tool("bg_task", map[string]any{"action": "stop", "id": "bg-1"})
					if failure != "" {
						outcome = "error"
						eq(t, strings.Contains(failure, "fixture_systemctl.exit=1"), w.stopFails, "the failure carries the unit's error")
					} else {
						outcome = text(t, raw).Details["action"].(string)
					}
				}
				wantOutcome := "stop"
				if shutdown {
					wantOutcome = ""
				} else if w.stopFails {
					wantOutcome = "error"
				}
				eq(t, outcome, wantOutcome, "outcome")
				var afterSignals, afterUnits []string
				switch {
				case w.resource:
					afterSignals = []string{}
					afterUnits = term
					if shutdown {
						afterUnits = append(term, kill...)
					}
				case shutdown:
					afterSignals = []string{"SIGTERM", "SIGKILL"}
					afterUnits = []string{}
				default:
					afterSignals = []string{"SIGTERM"}
					afterUnits = []string{}
				}
				eq(t, signals(), afterSignals, "signals after")
				eq(t, orEmpty(systemctl()), afterUnits, "unit calls after")
				status, reason, _, _ = r.taskState("bg-1")
				eq(t, []string{status, reason}, []string{w.afterStatus, w.reason}, "state after")
				if w.escalate {
					r.rig.FireTimeout(t, 5000)
					r.rig.Settle()
					allSignals, allUnits := []string{}, []string{}
					if w.resource {
						allUnits = append(term, kill...)
					} else {
						allSignals = []string{"SIGTERM", "SIGKILL"}
					}
					eq(t, signals(), allSignals, "signals escalated")
					eq(t, orEmpty(systemctl()), allUnits, "unit calls escalated")
					status, reason, _, _ = r.taskState("bg-1")
					eq(t, []string{status, reason}, []string{w.afterStatus, w.reason}, "state escalated")
					// The child's close, by signal.
					_ = syscall.Kill(-pid, syscall.SIGKILL)
					r.waitStatus(t, "bg-1", w.final)
				}
				r.rig.DrainLogs()
				status, reason, _, _ = r.taskState("bg-1")
				eq(t, []string{status, reason}, []string{w.final, w.reason}, "final state")
				if !shutdown {
					r.Fire("session_shutdown", map[string]any{"reason": "quit"})
				}
				task, _ := r.rig.Task("bg-1")
				logFile := ""
				if task != nil {
					logFile, _ = task["logFile"].(string)
				} else {
					logFile = filepath.Join(os.Getenv("PI_BG_TASK_DIR"))
				}
				_ = logFile
				// A log write finishes on its own goroutine, so whether its stall deadline is cleared before or after the test's drain
				// cancels the flush window is not fixed: consecutive clears are compared as a set (see PORT.md).
				eq(t, clearsUnordered(r.rig.TimerEvents()), clearsUnordered(events[w.logFlush]), "timer events")
				eq(t, r.rig.ActiveTimers(), []string{}, "no timers remain")
			})
		}
	})
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

var _ = time.Second

// clearsUnordered sorts every run of consecutive "clear" events.
func clearsUnordered(events []string) []string {
	out := append([]string{}, events...)
	for i := 0; i < len(out); {
		j := i
		for j < len(out) && strings.HasPrefix(out[j], "clear ") {
			j++
		}
		sort.Strings(out[i:j])
		i = max(j, i+1)
	}
	return out
}
