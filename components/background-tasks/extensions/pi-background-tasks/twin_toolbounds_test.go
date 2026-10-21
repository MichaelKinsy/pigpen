package pi_background_tasks

import (
	"encoding/json"
	"strings"
	"testing"
)

// Twins of log-tool-result-bounds.test.ts and orphan-reconcile-effects.test.ts: the registered log tool result and the orphan
// watcher's finalization, each against the real runtime and the real hooks of the port.

func TestTwinLogToolResultBounds(t *testing.T) {
	tw(t, "log-tool-result-bounds", "registered log tool result rows", func(t *testing.T) {
		testAgent(t)
		capn := int(defaultLogTailMaxChars)
		fieldCap := wakeManifestFieldMaxChars
		logFile := "/tmp/kendex-pi-bg/bg-log-1-1700000000000.log"
		marker := "retained log tail\n"
		tail := marker + strings.Repeat("z", capn-len(marker)-1) + "!"
		longPath := "/tmp/" + strings.Repeat("L", 5000)
		huge := strings.Repeat("z", capn*3) + tail
		rows := []struct {
			name, tool, output string
			huge               bool
			path               string
		}{
			{"bg_task huge log and metadata", "bg_task", huge, true, logFile},
			{"bg_status huge log and metadata", "bg_status", huge, true, logFile},
			{"bg_task long log path", "bg_task", huge, true, longPath},
			{"bg_status long log path", "bg_status", huge, true, longPath},
			{"bg_task small output", "bg_task", "all good\n", false, logFile},
			{"bg_status small output", "bg_status", "all good\n", false, logFile},
			{"bg_task empty output", "bg_task", "", false, logFile},
			{"bg_status empty output", "bg_status", "", false, logFile},
		}
		for _, row := range rows {
			t.Run(row.name, func(t *testing.T) {
				rt := newRuntime(realTimers{})
				tk := fakeTask(func(x *task) {
					x.ID, x.Pid, x.LogFile = "bg-log-1", 4242, row.path
					x.Command, x.Title, x.Cwd = "echo log", "log", "/path/work"
					if row.huge {
						x.Command, x.Title, x.Cwd = strings.Repeat("Q", 200_000), strings.Repeat("T", 5000), "/"+strings.Repeat("C", 5000)
					}
					x.ProcIdent = &processIdentity{Pid: 4242, StartToken: "private-start", Comm: "private-command"}
					x.output = row.output
				})
				rt.tasks.set(tk)
				// The tool resolves the task by its id (bg_task) or its pid (bg_status), and shows it through the same log path.
				token := "bg-log-1"
				if row.tool == "bg_status" {
					token = "4242"
				}
				eq(t, rt.resolveTask(token) == tk, true, "the task is resolved by "+token)
				res := rt.logResult(tk)
				var out struct {
					Content string
					Details map[string]any
				}
				b, _ := json.Marshal(res)
				if err := json.Unmarshal(b, &out); err != nil {
					t.Fatal(err)
				}
				safePath := row.path
				if len(row.path) > fieldCap {
					safePath = "/tmp/" + strings.Repeat("L", fieldCap-6) + "…"
				}
				want := row.output
				if want == "" {
					want = "(empty)"
				}
				if row.huge {
					want = "[...truncated]\n" + tail + "\n\n[Background log truncated. Showing last " + itoa(capn) + " of " + itoa(len(row.output)) + " character(s). Full log: " + safePath + "]"
				}
				eq(t, out.Content, want, "content")
				task, _ := out.Details["task"].(map[string]any)
				wantTask := map[string]any{"id": "bg-log-1", "pid": float64(4242), "command": "echo log", "title": "log", "cwd": "/path/work", "logFile": safePath}
				if row.huge {
					wantTask["command"] = strings.Repeat("Q", fieldCap-1) + "…"
					wantTask["title"] = strings.Repeat("T", fieldCap-1) + "…"
					wantTask["cwd"] = "/" + strings.Repeat("C", fieldCap-2) + "…"
				}
				got := map[string]any{}
				for k := range wantTask {
					got[k] = task[k]
				}
				eq(t, got, wantTask, "task fields")
				eq(t, out.Details["action"], "log", "action")
				if row.huge {
					eq(t, out.Details["fullOutputPath"], safePath, "fullOutputPath")
					eq(t, out.Details["truncation"], map[string]any{"direction": "tail", "truncated": true, "fullOutputPath": safePath, "shownChars": float64(capn), "totalChars": float64(len(row.output))}, "truncation")
				} else {
					_, hasPath := out.Details["fullOutputPath"]
					_, hasTrunc := out.Details["truncation"]
					eq(t, []bool{hasPath, hasTrunc}, []bool{false, false}, "no truncation details")
				}
				eq(t, len(b) < 16_384, true, "the result is bounded")
				_, leaked := task["procIdent"]
				eq(t, leaked, false, "internal identity stays out of the result")
			})
		}
	})
}

func TestTwinOrphanReconcileEffects(t *testing.T) {
	tw(t, "orphan-reconcile-effects", "orphan reconciliation effect rows", func(t *testing.T) {
		rows := []struct {
			name, identity string
			finalized      int
			status, reason string
		}{
			{"gone orphan finalizes through the exact metadata hook order without signals", "gone", 1, "failed", "orphaned-pid-gone"},
			{"reused pid finalizes metadata without signalling the replacement", "reused", 1, "failed", "orphaned-pid-reused"},
			{"matching orphan remains running without hooks or signals", "matching", 0, "running", ""},
		}
		for _, row := range rows {
			t.Run(row.name, func(t *testing.T) {
				tk := orphanTask(4242, func(x *task) {
					x.ID = "bg-97"
					x.ProcIdent = &processIdentity{Comm: "bash", Pid: 4242, StartToken: "start-4242"}
				})
				var calls []string
				var events []string
				hooks := lifecycleHooks{
					clearTaskTimers:  func(*task) { calls = append(calls, "clearTaskTimers") },
					persistSnapshots: func() { calls = append(calls, "persistSnapshots") },
					refreshUI:        func() { calls = append(calls, "refreshUi") },
					rememberSnapshot: func(x *task) *snapshot { calls = append(calls, "rememberSnapshot"); return taskSnapshot(x) },
					sendTaskEvent: func(typ string, x *task) bool {
						calls = append(calls, "sendTaskEvent")
						events = append(events, typ+" "+x.ID+" "+x.Status+" "+x.TerminationReason+" "+map[bool]string{true: "same", false: "other"}[x == tk])
						return true
					},
				}
				signals := 0
				saved := sendKill
				sendKill = func(int, string, bool) error { signals++; return nil }
				defer func() { sendKill = saved }()
				w := newOrphanWatcher(orphanWatcherDeps{getTasks: func() []*task { return []*task{tk} }, hooks: hooks, timers: noTimers{t},
					identity: func(pid int) identityReading {
						switch row.identity {
						case "gone":
							return identityReading{kind: "gone"}
						case "reused":
							return identityReading{kind: "identity", identity: processIdentity{Comm: "bash", Pid: pid, StartToken: "reused-start"}}
						}
						return identityReading{kind: "identity", identity: processIdentity{Comm: "bash", Pid: pid, StartToken: "start-4242"}}
					}})
				eq(t, w.checkOnce(), row.finalized, "finalized")
				wantCalls, wantEvents := []string{}, []string{}
				if row.finalized == 1 {
					wantCalls = []string{"clearTaskTimers", "rememberSnapshot", "persistSnapshots", "sendTaskEvent", "rememberSnapshot", "persistSnapshots", "refreshUi"}
					wantEvents = []string{"exit bg-97 " + row.status + " " + row.reason + " same"}
				}
				eq(t, orEmptyStrings(calls), wantCalls, "hook order")
				eq(t, orEmptyStrings(events), wantEvents, "events")
				eq(t, []any{tk.Status, tk.TerminationReason, tk.ExitCode == nil, tk.closed, tk.ExitNotified, tk.stopReason, signals},
					[]any{row.status, row.reason, true, row.finalized == 1, row.finalized == 1, "", 0}, "task")
			})
		}
	})
}

func orEmptyStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
