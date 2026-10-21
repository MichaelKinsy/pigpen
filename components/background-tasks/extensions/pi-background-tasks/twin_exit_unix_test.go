//go:build !windows

package pi_background_tasks_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"

	bg "github.com/MichaelKinsy/pigpen/pi-background-tasks"
)

// Twins of the registered exit tests of the original (spawn-extension.ts modes restored-exit and exit-reads).

type exitMessage struct {
	eventType string
	tail      string
	truncated bool
	reason    string
	taskID    string
	deliverAs string
}

func (r *rigged) exitMessages() []exitMessage {
	var out []exitMessage
	for _, c := range r.CallsTo("sendMessage") {
		m, _ := c.Args["message"].(map[string]any)
		d, _ := m["details"].(map[string]any)
		if d["eventType"] != "exit" {
			continue
		}
		task, _ := d["task"].(map[string]any)
		opts, _ := c.Args["options"].(map[string]any)
		out = append(out, exitMessage{eventType: "exit", tail: fmt.Sprint(d["outputTail"]), truncated: d["outputTailTruncated"] == true,
			reason: fmt.Sprint(task["terminationReason"]), taskID: fmt.Sprint(task["id"]), deliverAs: fmt.Sprint(opts["deliverAs"])})
	}
	return out
}

func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

func TestTwinRestoredExit(t *testing.T) {
	tw(t, "restored-exit", "restored exits read their tail before delivery and suppress completions after clear or shutdown", func(t *testing.T) {
		var gone atomic.Bool
		bg.ProbeHooks(t, func(pid int) (string, string, bool) { return "12345", "fixture-child", !gone.Load() }, func(string) bool { return true })
		r := rigHost(t, rigOptions{noStart: true})
		long := strings.Repeat("x", 4000) + "TAIL"
		rows := []struct {
			action, output string
			truncated      bool
		}{
			{"delivered", long, true},
			{"short", "TAIL", false},
			{"exact", strings.Repeat("x", 1996) + "TAIL", false},
			{"unicode", strings.Repeat("😀", 1000) + "TAIL", true},
			{"orphan", long, true},
			{"clear", long, true},
			{"shutdown", long, true},
		}
		for _, row := range rows {
			gone.Store(false)
			logFile := filepath.Join(r.opts.Cwd, row.action+".log")
			if err := os.WriteFile(logFile, []byte(row.output), 0o644); err != nil {
				t.Fatal(err)
			}
			pid, status := 0, "completed"
			if row.action == "orphan" {
				pid, status = 4242, "running"
			}
			task := map[string]any{"id": "bg-1", "command": "restored", "cwd": r.opts.Cwd, "exitCode": 0, "exitNotified": false, "logFile": logFile, "notifyOnExit": true,
				"notifyOnOutput": false, "outputBytes": len(row.output), "pid": pid, "sessionId": row.action, "startedAt": 1, "status": status, "title": "restored", "updatedAt": 1,
				"procIdent": map[string]any{"pid": 4242, "comm": "fixture-child", "startToken": "12345"}}
			r.setSession(row.action, []map[string]any{{"type": "custom", "customType": "kendex-background-tasks:state", "data": map[string]any{"tasks": []any{task}}}})
			before := len(r.exitMessages())
			// The original's tail read is asynchronous and the clear or shutdown follows the start at once; the Go read could finish first,
			// so the suppression rows hold the log open until the clear or shutdown has run.
			var opens *bg.LogOpens
			if row.action == "clear" || row.action == "shutdown" {
				opens = r.rig.HoldLogOpens()
			}
			r.Fire("session_start", map[string]any{"reason": "startup"})
			if row.action == "clear" {
				r.Tool("bg_task", map[string]any{"action": "clear"})
			}
			if row.action == "shutdown" {
				r.Fire("session_shutdown", map[string]any{"reason": "quit"})
			}
			if row.action == "orphan" {
				time.Sleep(50 * time.Millisecond)
				eq(t, len(r.exitMessages()), before, "a live restored orphan must not emit an exit")
				gone.Store(true)
				r.rig.FireInterval(t, 30000)
			}
			suppressed := row.action == "clear" || row.action == "shutdown"
			if opens != nil {
				opens.Release(-1)
			}
			if suppressed {
				time.Sleep(200 * time.Millisecond)
			} else {
				waitFor(t, row.action+" exit", func() bool { return len(r.exitMessages()) > before })
			}
			got := r.exitMessages()
			eq(t, len(got), before+map[bool]int{true: 0, false: 1}[suppressed], row.action)
			if !suppressed {
				m := got[before]
				eq(t, m.truncated, row.truncated, "exit omission metadata: "+row.action)
				eq(t, strings.HasPrefix(m.tail, "[...truncated]\n"), row.truncated, "exit omission marker: "+row.action)
				eq(t, strings.HasSuffix(m.tail, "TAIL"), true, "the tail ends with the log's end")
				eq(t, utf16Len(m.tail) <= 2015, true, "bounded tail")
				if row.action == "delivered" || row.action == "orphan" {
					eq(t, m.tail, "[...truncated]\n"+strings.Repeat("x", 1996)+"TAIL", "tail")
				}
				if row.action == "unicode" {
					eq(t, m.tail, "[...truncated]\n"+strings.Repeat("😀", 998)+"TAIL", "restored exit character limit")
				}
				if !row.truncated {
					eq(t, m.tail, row.output, "whole output")
				}
				if row.action == "orphan" {
					eq(t, m.reason, "orphaned-pid-gone", "reason")
				}
			}
			if row.action != "shutdown" {
				r.Fire("session_shutdown", map[string]any{"reason": "quit"})
			}
		}
		eq(t, len(r.exitMessages()), 5, "delivered")
		eq(t, len(r.CallsTo("exec")), 0, "restore never spawns a command")
	})
	tskip(t, "restored-exit", "must-fail: dropping the asynchronous exit tail fails the registered lifecycle suite", "a must-fail source mutation of the original; the Go mutant restored-exit-tail-dropped (port/mutations.json) must fail TestTwinRestoredExit and TestTwinExitReads")
	tskip(t, "restored-exit", "must-fail: dropping disk omission information loses exit-tail metadata", "a must-fail source mutation of the original; the Go mutant restored-exit-disk-omission-dropped (port/mutations.json) must fail TestTwinRestoredExit and TestTwinExitReads")
	tskip(t, "restored-exit", "must-fail: dropping the forced omission marker loses exit-tail metadata", "a must-fail source mutation of the original; the Go mutant restored-exit-forced-marker-dropped (port/mutations.json) must fail TestTwinRestoredExit and TestTwinExitReads")
	tskip(t, "restored-exit", "must-fail: forgetting bounded-read omissions loses exit-tail metadata", "a must-fail source mutation of the original; the Go mutant restored-exit-bounded-read-omission-forgotten (port/mutations.json) must fail TestTwinRestoredExit and TestTwinExitReads")
	tskip(t, "restored-exit", "must-fail: unprotected queued exits fail the producer-created retention check", "a must-fail source mutation of the original; the Go mutant restored-exit-queue-unprotected (port/mutations.json) must fail TestTwinRestoredExit and TestTwinExitReads")
}

func TestTwinExitReads(t *testing.T) {
	tw(t, "restored-exit", "registered live, orphan and replay exits share admission and retain delivery and suppression", func(t *testing.T) {
		rows := []struct {
			producer, action              string
			acquired, delivered, retained int
		}{
			{"replay", "delivered", 64, 64, 50},
			{"live", "delivered", 64, 64, 50},
			{"orphan", "delivered", 64, 64, 50},
			{"mixed", "delivered", 66, 65, 50},
			{"replay", "clear", 4, 0, 0},
			{"replay", "shutdown", 4, 0, 64},
			{"replay", "replacement", 4, 0, 64},
		}
		for _, row := range rows {
			t.Run(row.producer+": "+row.action, func(t *testing.T) {
				var gone atomic.Bool
				bg.ProbeHooks(t, func(pid int) (string, string, bool) { return "12345", "fixture-child", !gone.Load() }, func(string) bool { return true })
				const session = "spawn-hardening-private-session"
				r := rigHost(t, rigOptions{session: session})
				flag := filepath.Join(t.TempDir(), "go")
				t.Cleanup(func() { _ = os.WriteFile(flag, nil, 0o644) })
				opens := r.rig.HoldLogOpens()
				const count = 64
				emoji := strings.Repeat("😀", 4000) + "TAIL"
				wait := "while [ ! -e '" + flag + "' ]; do sleep 0.05; done"
				for i := 0; i < count; i++ {
					command := wait
					if row.producer != "live" {
						command = "printf '%s' '" + emoji + "'; " + wait
					}
					_, failure := r.Tool("bg_task", map[string]any{"action": "spawn", "command": command, "notifyOnExit": true, "timeoutSeconds": 0})
					eq(t, failure, "", "spawn")
				}
				if row.producer != "live" {
					waitFor(t, "every task's output", func() bool {
						for i := 1; i <= count; i++ {
							task, _ := r.rig.Task(fmt.Sprintf("bg-%d", i))
							if task["outputBytes"] != float64(len(emoji)) {
								return false
							}
						}
						return true
					})
				}
				r.rig.DrainLogs()
				saved := func() []map[string]any {
					b, err := os.ReadFile(bg.SidecarStatePath(session))
					if err != nil {
						t.Fatal(err)
					}
					var doc struct {
						Tasks []map[string]any `json:"tasks"`
					}
					_ = json.Unmarshal(b, &doc)
					return doc.Tasks
				}
				eq(t, len(saved()), count, "saved tasks")
				for _, task := range saved() {
					if task["status"] != "running" || task["exitNotified"] == true {
						t.Fatalf("a spawned task is saved as %v", task)
					}
				}
				if row.producer == "replay" || row.producer == "mixed" {
					r.Fire("session_shutdown", map[string]any{"reason": "quit"})
				}
				produced := saved()
				eq(t, len(produced), count, "produced")
				wantStatus, wantReason := "stopped", "session-shutdown"
				if row.producer == "live" || row.producer == "orphan" {
					wantStatus, wantReason = "running", ""
				}
				for _, task := range produced {
					reason, _ := task["terminationReason"].(string)
					if task["status"] != wantStatus || reason != wantReason || task["exitNotified"] == true {
						t.Fatalf("a produced task is %v, want %s/%s unnotified", task, wantStatus, wantReason)
					}
				}
				eq(t, len(r.exitMessages()), 0, "no exit before the producer runs")
				if row.producer == "live" {
					if err := os.WriteFile(flag, nil, 0o644); err != nil {
						t.Fatal(err)
					}
				} else {
					r.Fire("session_start", map[string]any{"reason": "startup"})
					if row.producer == "orphan" {
						gone.Store(true)
						r.rig.FireInterval(t, 30000)
					}
				}
				waitFor(t, "the first reads", func() bool { return len(opens.Requested()) == 4 && opens.Held() == 4 })
				time.Sleep(100 * time.Millisecond)
				eq(t, len(opens.Requested()), 4, "exit acquisition must be bounded before any read settles")
				eq(t, len(r.exitMessages()), 0, "nothing delivered before a read settles")
				eq(t, len(saved()), count, "queued exits must remain protected from history eviction")
				for _, task := range saved() {
					if task["exitNotified"] == true {
						t.Fatal("acquisition is not acknowledgement")
					}
				}
				initial := len(opens.Requested())
				if row.action == "delivered" {
					total := count
					var logDone chan struct{}
					if row.producer == "mixed" {
						_, failure := r.Tool("bg_task", map[string]any{"action": "spawn", "command": "exit 0", "notifyOnExit": true})
						eq(t, failure, "", "spawn bg-65")
						r.waitStatus(t, "bg-65", "completed")
						// The tool's own read of bg-65 starts now, as in the original; the fake host's blocking tool call would give up
						// after 10 s, so it is sent without waiting.
						logDone = r.toolAsync("bg_task", map[string]any{"action": "log", "id": "bg-65"})
						total++
					}
					bg65Opens := func() int {
						n := 0
						for _, p := range opens.Requested() {
							if strings.Contains(filepath.Base(p), "bg-65-") {
								n++
							}
						}
						return n
					}
					bg65Delivered := func() bool {
						for _, m := range r.exitMessages() {
							if m.taskID == "bg-65" {
								return true
							}
						}
						return false
					}
					toolDone := func() bool {
						select {
						case <-logDone:
							return true
						default:
							return false
						}
					}
					toolTaken := false
					for delivered := 0; delivered < total; delivered++ {
						waitFor(t, "a held open", func() bool { return opens.Held() > 0 })
						opens.Release(1)
						for {
							waitFor(t, fmt.Sprintf("an exit delivery %d (requested %d, held %d, passed %d, active %d)", delivered+1, len(opens.Requested()), opens.Held(), opens.Passed(), opens.Active()), func() bool {
								return len(r.exitMessages()) == delivered+1 || (logDone != nil && !toolTaken && toolDone())
							})
							if len(r.exitMessages()) == delivered+1 {
								break
							}
							// The release went to the tool's own reopen of bg-65, which only follows the exit read of that task.
							toolTaken = true
							waitFor(t, "a held open", func() bool { return opens.Held() > 0 })
							opens.Release(1)
						}
						if logDone != nil && !bg65Delivered() {
							// The tool's read joins the exit read of bg-65: the log is not opened a second time before that exit is delivered.
							eq(t, bg65Opens() <= 1, true, "the tool read must wait for the exit read of its task")
						}
						eq(t, opens.MaxActive() <= 4, true, "aggregate acquisition must stay bounded through delivery")
						notified := map[string]bool{}
						for _, m := range r.exitMessages() {
							notified[m.taskID] = true
						}
						retained := map[string]bool{}
						for _, s := range saved() {
							retained[s["id"].(string)] = true
						}
						for _, p := range produced {
							if !notified[p["id"].(string)] && !retained[p["id"].(string)] {
								t.Fatalf("queued exit %v vanished from every history pass", p["id"])
							}
						}
					}
					if logDone != nil {
						if !toolTaken {
							// The tool's read of bg-65 started after the task's exit read did, so it joined that read and opens the log once
							// more only after it: a single held open, taken last.
							waitFor(t, "mixed tool read must acquire", func() bool { return opens.Held() == 1 })
							opens.Release(1)
						}
						<-logDone
					}
					distinct := map[string]bool{}
					for _, p := range opens.Requested() {
						distinct[p] = true
					}
					eq(t, len(distinct), total, "each saved task reads once")
					ids := map[string]bool{}
					for _, m := range r.exitMessages() {
						ids[m.taskID] = true
					}
					eq(t, len(ids), total, "each saved task delivers once")
					for _, m := range r.exitMessages() {
						quiet := row.producer == "live" || m.taskID == "bg-65"
						wantReason := "session-shutdown"
						if quiet {
							wantReason = "self-exit"
						} else if row.producer == "orphan" {
							wantReason = "orphaned-pid-gone"
						}
						eq(t, m.reason, wantReason, "reason of "+m.taskID)
						wantTail := "[...truncated]\n" + strings.Repeat("😀", 998) + "TAIL"
						if quiet {
							wantTail = ""
						}
						eq(t, m.tail, wantTail, "tail of "+m.taskID)
						eq(t, m.truncated, !quiet, "truncation of "+m.taskID)
					}
					// The bound runs after the last wake is acknowledged.
					waitFor(t, "the history bound", func() bool {
						if len(saved()) != bg.MaxFinishedTasks {
							return false
						}
						for _, task := range saved() {
							if task["exitNotified"] != true {
								return false
							}
						}
						return true
					})
				} else {
					switch row.action {
					case "clear":
						r.Tool("bg_task", map[string]any{"action": "clear"})
					case "shutdown":
						r.Fire("session_shutdown", map[string]any{"reason": "quit"})
					case "replacement":
						r.setSession("replacement", nil)
						r.Fire("session_start", map[string]any{"reason": "new"})
						_, failure := r.Tool("bg_task", map[string]any{"action": "spawn", "command": "sleep 300", "notifyOnExit": true})
						eq(t, failure, "", "spawn")
					}
					opens.Release(-1)
					waitFor(t, "suppressed active reads to settle", func() bool { return opens.Passed() == initial })
					time.Sleep(100 * time.Millisecond)
					eq(t, len(opens.Requested()), initial, "suppressed queued tasks must not acquire a log")
					eq(t, len(r.exitMessages()), 0, "suppressed active tasks must not deliver an exit")
				}
				waitFor(t, "every log closed", func() bool { return opens.Active() == 0 })
				eq(t, []int{len(produced), initial, opens.MaxActive(), len(opens.Requested()), len(r.exitMessages()), len(saved())},
					[]int{64, 4, 4, row.acquired, row.delivered, row.retained}, "row evidence")
			})
		}
	})
}
