package pi_background_tasks

import (
	"io/fs"
	"strings"
	"sync"
	"testing"
	"time"
)

// Twins of the original's lifecycle, snapshot, restore and orphan-watcher tests.

func ptr[T any](v T) *T { return &v }

func TestTwinLifecycle(t *testing.T) {
	tw(t, "finalize-lifecycle", "finalize lifecycle outcomes", func(t *testing.T) {
		partialOutput := "Warning: GH_TOKEN/GITHUB_TOKEN failed gh auth; unsetting them and using gh keyring auth.\n"
		type expect struct {
			status, reason string
			exitNotified   *bool
			persists       *int
			output         string
			outputBytes    int64
		}
		rows := []struct {
			name        string
			mods        func(*task)
			exitCode    *int
			statusOver  string
			reasonOver  string
			sendReturns *bool
			secondClose bool
			expected    expect
		}{
			{"clean self-exit", nil, exitCode(0), "", "", nil, false, expect{status: "completed", reason: "self-exit"}},
			{"null exit code is external", nil, nil, "", "", nil, false, expect{status: "failed", reason: "external"}},
			{"nonzero self-exit", nil, exitCode(1), "", "", nil, false, expect{status: "failed", reason: "self-exit"}},
			{"user stop derives extension-stop", func(x *task) { x.stopReason = "user" }, nil, "", "", nil, false, expect{status: "stopped", reason: "extension-stop"}},
			{"timeout stop derives timeout", func(x *task) { x.stopReason = "timeout" }, nil, "", "", nil, false, expect{status: "timed_out", reason: "timeout"}},
			{"status override wins over user stop and successful exit", func(x *task) { x.stopReason = "user" }, exitCode(0), "failed", "", nil, false, expect{status: "failed", reason: "extension-stop"}},
			{"sender false retains pending exit notification", nil, exitCode(0), "", "", ptr(false), false, expect{status: "completed", reason: "self-exit", exitNotified: ptr(false), persists: ptr(1)}},
			// The sender result is injected; this row does not test the host sender's setting gate.
			{"sender false with notifyOnExit disabled", func(x *task) { x.NotifyOnExit = false }, exitCode(0), "", "", ptr(false), false, expect{status: "completed", reason: "self-exit", exitNotified: ptr(false), persists: ptr(1)}},
			{"second close leaves task and every hook unchanged", nil, exitCode(0), "", "", nil, true, expect{status: "completed", reason: "self-exit"}},
			{"partial output survives external termination", func(x *task) { x.output, x.OutputBytes = partialOutput, 89 }, nil, "", "", nil, false, expect{status: "failed", reason: "external", output: partialOutput, outputBytes: 89}},
			{"zero output still sends an exit", func(x *task) { x.output, x.OutputBytes = "", 0 }, exitCode(0), "", "", nil, false, expect{status: "completed", reason: "self-exit"}},
			{"pre-stamped extension-stop survives finalize", func(x *task) { x.stopReason, x.TerminationReason = "user", "extension-stop" }, nil, "", "", nil, false, expect{status: "stopped", reason: "extension-stop"}},
			{"shutdown stop derives session-shutdown", func(x *task) { x.stopReason = "shutdown" }, nil, "", "", nil, false, expect{status: "stopped", reason: "session-shutdown"}},
			{"explicit reason wins over pre-stamped and derived reason", func(x *task) { x.stopReason, x.TerminationReason = "user", "extension-stop" }, nil, "", "orphaned-pid-reused", nil, false, expect{status: "stopped", reason: "orphaned-pid-reused"}},
			{"pre-stamped reason wins over a different derivation", func(x *task) { x.TerminationReason = "extension-stop" }, exitCode(0), "", "", nil, false, expect{status: "completed", reason: "extension-stop"}},
		}
		for _, row := range rows {
			tk := fakeTask()
			if row.mods != nil {
				row.mods(tk)
			}
			sends := true
			if row.sendReturns != nil {
				sends = *row.sendReturns
			}
			rec, hooks := recordingHooks(sends)
			result := finalizeTaskLifecycle(tk, row.exitCode, hooks, row.statusOver, row.reasonOver)
			persists := 2
			if row.expected.persists != nil {
				persists = *row.expected.persists
			}
			notified := true
			if row.expected.exitNotified != nil {
				notified = *row.expected.exitNotified
			}
			type view struct {
				SameTask             bool
				Status               string
				ExitCode             *int
				ExitNotified, Closed bool
				Reason, Output       string
				OutputBytes          int64
				Hooks                observed
			}
			observe := func() view {
				return view{result == tk, tk.Status, tk.ExitCode, tk.ExitNotified, tk.closed, tk.TerminationReason, tk.output, tk.OutputBytes, rec.observe([]*task{tk})}
			}
			want := view{true, row.expected.status, row.exitCode, notified, true, row.expected.reason, row.expected.output, row.expected.outputBytes,
				observed{Events: []observedEvent{{"exit", "bg-3", row.expected.reason, true}}, Persists: persists, Remembers: persists, Refreshes: 1, TimerClears: 1}}
			eq(t, observe(), want, row.name)
			if row.secondClose {
				before := *tk
				again := finalizeTaskLifecycle(tk, exitCode(99), hooks, "", "")
				eq(t, observe(), want, row.name+": after the second close")
				eq(t, tk.snapshot, before.snapshot, row.name+": the task is unchanged")
				eq(t, again == tk, true, row.name+": same task")
			}
		}
	})

	tw(t, "missed-exit-selection", "selectMissedExits selects exact task IDs from notification and status inputs", func(t *testing.T) {
		type cand struct {
			id, status string
			notify     bool
			notified   *bool
		}
		rows := []struct {
			name     string
			tasks    []cand
			expected []string
		}{
			{"mixed tasks retain selection order", []cand{
				{"bg-1", "running", true, ptr(false)}, {"bg-2", "stopped", true, ptr(false)}, {"bg-3", "completed", true, ptr(true)},
				{"bg-4", "failed", false, ptr(false)}, {"bg-5", "timed_out", true, ptr(false)}}, []string{"bg-2", "bg-5"}},
			{"running task with no exit notification is excluded", []cand{{"bg-1", "running", true, ptr(false)}}, nil},
			{"stopped task with notification enabled is selected", []cand{{"bg-2", "stopped", true, ptr(false)}}, []string{"bg-2"}},
			{"completed task with prior notification is excluded", []cand{{"bg-3", "completed", true, ptr(true)}}, nil},
			{"failed task with notifications disabled is excluded", []cand{{"bg-4", "failed", false, ptr(false)}}, nil},
			{"timed out task with notification enabled is selected", []cand{{"bg-5", "timed_out", true, ptr(false)}}, []string{"bg-5"}},
			{"completed task with undefined notification is excluded", []cand{{"bg-1", "completed", true, nil}}, nil},
			{"stopped task with undefined notification is excluded", []cand{{"bg-2", "stopped", true, nil}}, nil},
		}
		for _, row := range rows {
			var list []candidateRow
			for _, c := range row.tasks {
				list = append(list, candidateRow{c.id, c.status, c.notify, c.notified})
			}
			var ids []string
			for _, c := range selectMissedExits(list) {
				ids = append(ids, c.id)
			}
			eq(t, ids, row.expected, row.name)
		}
	})

	tw(t, "replay-missed-exits", "missed exit replay outcomes", func(t *testing.T) {
		type spec struct {
			mods func(*task)
		}
		mk := func(id, status string, notified *bool, exit *int, notify *bool) func(*task) {
			return func(x *task) {
				if id != "" {
					x.ID = id
				}
				x.Status = status
				if notified != nil {
					x.ExitNotified = *notified
				}
				x.ExitCode = exit
				if notify != nil {
					x.NotifyOnExit = *notify
				}
				x.OutputBytes = 0
			}
		}
		f, tr := false, true
		rows := []struct {
			name        string
			tasks       []func(*task)
			sendReturns *bool
			replayed    int
			eventIDs    []string
			notified    []bool
			persists    int
		}{
			{"mixed tasks replay only the pending enabled terminal exit", []func(*task){
				mk("bg-1", "running", nil, nil, nil), mk("bg-2", "stopped", &f, nil, &tr), mk("bg-3", "completed", &tr, exitCode(0), nil), mk("bg-4", "failed", &f, exitCode(1), &f)},
				nil, 1, []string{"bg-2"}, []bool{false, true, true, false}, 1},
			{"running task causes no event or persistence", []func(*task){mk("", "running", nil, nil, nil)}, nil, 0, nil, []bool{false}, 0},
			{"already notified terminal task is excluded", []func(*task){mk("", "completed", &tr, exitCode(0), nil)}, nil, 0, nil, []bool{true}, 0},
			{"disabled terminal notification is excluded", []func(*task){mk("", "failed", nil, exitCode(1), &f)}, nil, 0, nil, []bool{false}, 0},
			{"pending stopped exit is replayed", []func(*task){mk("", "stopped", nil, nil, nil)}, nil, 1, []string{"bg-3"}, []bool{true}, 1},
			{"sender false leaves notification pending", []func(*task){mk("bg-2", "stopped", &f, nil, nil)}, &f, 0, []string{"bg-2"}, []bool{false}, 0},
			{"empty task collection causes no effects", nil, nil, 0, nil, []bool{}, 0},
			{"each successful replay persists its acknowledgement", []func(*task){mk("bg-1", "completed", nil, exitCode(0), nil), mk("bg-2", "failed", nil, exitCode(1), nil)}, nil, 2, []string{"bg-1", "bg-2"}, []bool{true, true}, 2},
		}
		for _, row := range rows {
			var tasks []*task
			for _, m := range row.tasks {
				tasks = append(tasks, fakeTask(m))
			}
			sends := true
			if row.sendReturns != nil {
				sends = *row.sendReturns
			}
			rec, hooks := recordingHooks(sends)
			before := 0
			for _, x := range tasks {
				if x.ExitNotified {
					before++
				}
			}
			replayMissedExitsLifecycle(tasks, hooks)
			after := 0
			notified := []bool{}
			for _, x := range tasks {
				notified = append(notified, x.ExitNotified)
				if x.ExitNotified {
					after++
				}
			}
			eq(t, after-before, row.replayed, row.name+": replayed")
			eq(t, notified, row.notified, row.name+": notified")
			events := []observedEvent{}
			for _, id := range row.eventIDs {
				events = append(events, observedEvent{"exit", id, "", true})
			}
			eq(t, rec.observe(tasks), observed{Events: events, Persists: row.persists, Remembers: row.persists}, row.name+": hooks")
		}
	})

	tw(t, "snapshot-serialization", "taskSnapshot serializes notification, session and termination fields", func(t *testing.T) {
		rows := []struct {
			name     string
			mods     func(*task)
			notified bool
			session  string
			reason   string
		}{
			{"completed task retains exit notification", func(x *task) {
				x.Status, x.ExitCode, x.ExitNotified, x.OutputBytes = "completed", exitCode(0), true, 89
			}, true, "sess-1", ""},
			{"undefined exit notification becomes false", func(x *task) { x.OutputBytes = 89 }, false, "sess-1", ""},
			{"session identity survives serialization", func(x *task) { x.SessionID, x.OutputBytes = "sess-1", 89 }, false, "sess-1", ""},
			{"extension stop reason survives serialization", func(x *task) {
				x.ExitCode, x.Status, x.TerminationReason, x.SessionID, x.OutputBytes = exitCode(0), "stopped", "extension-stop", "sess-A", 12
			}, false, "sess-A", "extension-stop"},
			{"undefined termination reason stays undefined", func(x *task) { x.SessionID, x.OutputBytes = "sess-A", 12 }, false, "sess-A", ""},
		}
		for _, row := range rows {
			s := taskSnapshot(fakeTask(row.mods))
			eq(t, [3]any{s.ExitNotified, s.SessionID, s.TerminationReason}, [3]any{row.notified, row.session, row.reason}, row.name)
		}
	})

	tw(t, "snapshot-restore", "restoredTaskFromSnapshot restores task state and missed exit eligibility", func(t *testing.T) {
		type want struct {
			status, stopReason   string
			closed, exitNotified bool
			updatedAt            int64
			reason               string
			missed               []string
			outputBytes          int64
			lastAnnounced        int
		}
		gone := func(int) identityReading { return reading(nil) }
		same := func(pid int) identityReading { return reading(fakeIdent(pid)) }
		running := func(x *snapshot) { x.Status = "running" }
		rows := []struct {
			name string
			mods func(*snapshot)
			omit string
			opts restoreOptions
			want want
		}{
			{"dead running process clears its old exit notification", func(x *snapshot) {
				running(x)
				x.ExitNotified, x.ProcIdent, x.OutputBytes = true, fakeIdent(2409160), 89
			}, "",
				restoreOptions{now: 1_700_000_100_000, identity: gone, sessionID: "sess-1", hasSession: true},
				want{"stopped", "shutdown", true, false, 1_700_000_100_000, "reconcile-on-restart", []string{"bg-3"}, 89, 89}},
			{"matching live process stays running with its original timestamp", func(x *snapshot) { running(x); x.Pid, x.ProcIdent, x.OutputBytes = 4242, fakeIdent(4242), 89 }, "",
				restoreOptions{now: 1_700_000_200_000, identity: same, sessionID: "sess-1", hasSession: true},
				want{"running", "", false, false, 1_700_000_000_000, "", nil, 89, 89}},
			{"reused process ID becomes stopped and eligible for exit replay", func(x *snapshot) {
				running(x)
				x.Pid, x.ExitNotified, x.ProcIdent, x.OutputBytes = 12345, false, fakeIdent(12345), 89
			}, "", restoreOptions{now: 1_700_000_300_000, sessionID: "sess-1", hasSession: true, identity: func(pid int) identityReading {
				return reading(&processIdentity{Pid: pid, StartToken: "start-RECYCLED", Comm: "unrelated"})
			}}, want{"stopped", "shutdown", true, false, 1_700_000_300_000, "reconcile-on-restart", []string{"bg-3"}, 89, 89}},
			{"executable name drift with the same start token stays running", func(x *snapshot) {
				running(x)
				x.Pid, x.Command, x.OutputBytes, x.ProcIdent = 4242, "/bin/bash -lc 'sleep 5'", 89, &processIdentity{Pid: 4242, StartToken: "19283746", Comm: "bash"}
			}, "", restoreOptions{now: 1_700_000_100_000, sessionID: "sess-1", hasSession: true, identity: func(pid int) identityReading {
				return reading(&processIdentity{Pid: pid, StartToken: "19283746", Comm: "sleep"})
			}}, want{"running", "", false, false, 1_700_000_000_000, "", nil, 89, 89}},
			{"absent recorded identity uses live process ID", func(x *snapshot) { running(x); x.Pid, x.OutputBytes = 4242, 89 }, "procIdent",
				restoreOptions{now: 1_700_000_100_000, identity: same, sessionID: "sess-1", hasSession: true},
				want{"running", "", false, false, 1_700_000_000_000, "", nil, 89, 89}},
			{"completed and notified snapshot keeps its terminal state", func(x *snapshot) {
				x.Status, x.ExitNotified, x.ExitCode, x.OutputBytes = "completed", true, exitCode(0), 89
			}, "",
				restoreOptions{now: 1_700_000_100_000, identity: gone},
				want{"completed", "", true, true, 1_700_000_000_000, "", nil, 89, 89}},
			{"terminal snapshot with absent exit notification becomes replay eligible", func(x *snapshot) {
				x.Status, x.ExitCode, x.NotifyOnExit, x.OutputBytes = "completed", exitCode(0), true, 89
			}, "exitNotified",
				restoreOptions{now: 1_700_000_100_000, identity: gone},
				want{"completed", "", true, false, 1_700_000_000_000, "", []string{"bg-3"}, 89, 89}},
			{"stopped snapshot explicitly never notified stays replay eligible", func(x *snapshot) { x.Status, x.ExitNotified, x.OutputBytes = "stopped", false, 89 }, "",
				restoreOptions{now: 1_700_000_100_000, identity: gone},
				want{"stopped", "", true, false, 1_700_000_000_000, "", []string{"bg-3"}, 89, 89}},
			{"foreign session snapshot cannot replay an exit", func(x *snapshot) {
				running(x)
				x.SessionID, x.ExitNotified, x.ProcIdent, x.OutputBytes = "sess-OTHER", false, fakeIdent(2409160), 89
			}, "", restoreOptions{now: 1_700_000_100_000, identity: gone, sessionID: "sess-1", hasSession: true},
				want{"stopped", "shutdown", true, true, 1_700_000_100_000, "reconcile-on-restart", nil, 89, 89}},
			{"dead running snapshot with absent session identity is same session", func(x *snapshot) { running(x); x.OutputBytes = 89 }, "sessionId",
				restoreOptions{now: 1_700_000_100_000, identity: gone, sessionID: "sess-1", hasSession: true},
				want{"stopped", "shutdown", true, false, 1_700_000_100_000, "reconcile-on-restart", []string{"bg-3"}, 89, 89}},
			{"stalled review waiter incident selects the restored task ID", func(x *snapshot) {
				running(x)
				x.ID, x.ExitCode, x.OutputBytes, x.ExitNotified, x.NotifyOnExit, x.ProcIdent = "bg-3", nil, 89, false, true, fakeIdent(2409160)
			}, "", restoreOptions{now: 1_700_000_100_000, identity: gone, sessionID: "sess-1", hasSession: true},
				want{"stopped", "shutdown", true, false, 1_700_000_100_000, "reconcile-on-restart", []string{"bg-3"}, 89, 89}},
			{"surviving review waiter incident has no false exit selection", func(x *snapshot) {
				running(x)
				x.ID, x.Pid, x.NotifyOnExit, x.ProcIdent, x.OutputBytes = "bg-3", 4242, true, fakeIdent(4242), 89
			}, "", restoreOptions{now: 1_700_000_100_000, identity: same, sessionID: "sess-1", hasSession: true},
				want{"running", "", false, false, 1_700_000_000_000, "", nil, 89, 89}},
			{"dead process restore stamps reconcile reason", func(x *snapshot) {
				running(x)
				x.Pid, x.SessionID, x.OutputBytes, x.ProcIdent = 4242, "sess-A", 12, &processIdentity{Comm: "bash", Pid: 4242, StartToken: "start-4242"}
			}, "", restoreOptions{now: 1_700_000_100_000, identity: gone, sessionID: "sess-A", hasSession: true},
				want{"stopped", "shutdown", true, false, 1_700_000_100_000, "reconcile-on-restart", []string{"bg-3"}, 12, 12}},
			{"live process restore keeps an undefined termination reason", func(x *snapshot) {
				running(x)
				x.Pid, x.SessionID, x.OutputBytes, x.ProcIdent = 4242, "sess-A", 12, &processIdentity{Comm: "bash", Pid: 4242, StartToken: "start-4242"}
			}, "", restoreOptions{now: 1_700_000_100_000, sessionID: "sess-A", hasSession: true, identity: func(pid int) identityReading {
				return reading(&processIdentity{Comm: "bash", Pid: pid, StartToken: "start-4242"})
			}}, want{"running", "", false, false, 1_700_000_000_000, "", nil, 12, 12}},
			{"terminal snapshot retains a populated termination reason", func(x *snapshot) {
				x.ExitCode, x.ExitNotified, x.Status, x.TerminationReason, x.SessionID, x.OutputBytes = exitCode(0), true, "completed", "self-exit", "sess-A", 12
			}, "", restoreOptions{now: 1_700_000_100_000, identity: gone, sessionID: "sess-A", hasSession: true},
				want{"completed", "", true, true, 1_700_000_000_000, "self-exit", nil, 12, 12}},
		}
		for _, row := range rows {
			s := fakeSnapshot(row.mods)
			switch row.omit {
			case "procIdent":
				s.ProcIdent = nil
			case "exitNotified":
				s.ExitNotified = false
			case "sessionId":
				s.SessionID = ""
			}
			restored := restoredTaskFromSnapshot(s, row.opts)
			var missed []string
			for _, m := range selectMissedExits([]*task{restored}) {
				missed = append(missed, m.ID)
			}
			got := want{restored.Status, restored.stopReason, restored.closed, restored.ExitNotified, restored.UpdatedAt, restored.TerminationReason, missed, restored.OutputBytes, restored.lastAnnouncedLen}
			eq(t, got, row.want, row.name)
			eq(t, restored.restored, true, row.name+": restored")
		}
	})

	tw(t, "restore-replay", "restore followed by missed exit replay", func(t *testing.T) {
		rows := []struct {
			name     string
			mods     func(*snapshot)
			identity *processIdentity
			status   string
			closed   bool
			before   bool
			after    bool
			reason   string
			replayed int
			eventIDs []string
		}{
			{"dead same-session running snapshot is stopped before exit replay", func(x *snapshot) {
				x.ID, x.Status, x.ExitCode, x.OutputBytes, x.ExitNotified, x.NotifyOnExit, x.ProcIdent = "bg-3", "running", nil, 89, false, true, fakeIdent(2409160)
			}, nil, "stopped", true, false, true, "reconcile-on-restart", 1, []string{"bg-3"}},
			{"matching live identity remains running without an exit replay", func(x *snapshot) {
				x.ID, x.Status, x.Pid, x.NotifyOnExit, x.ProcIdent = "bg-3", "running", 4242, true, fakeIdent(4242)
			}, fakeIdent(4242), "running", false, false, false, "", 0, nil},
			{"foreign-session snapshot is ineligible before replay", func(x *snapshot) {
				x.ID, x.Status, x.ExitNotified, x.NotifyOnExit, x.SessionID = "bg-other", "running", false, true, "sess-OTHER"
			}, nil, "stopped", true, true, true, "reconcile-on-restart", 0, nil},
		}
		for _, row := range rows {
			rec, hooks := recordingHooks()
			restored := restoredTaskFromSnapshot(fakeSnapshot(row.mods), restoreOptions{identity: func(int) identityReading { return reading(row.identity) }, sessionID: "sess-1", hasSession: true, now: 1_700_000_100_000})
			// Capture the restore result before replay can change exitNotified.
			eq(t, [3]any{restored.Status, restored.closed, restored.ExitNotified}, [3]any{row.status, row.closed, row.before}, row.name+": before")
			replayMissedExitsLifecycle([]*task{restored}, hooks)
			eq(t, restored.ExitNotified, row.after, row.name+": after")
			events := []observedEvent{}
			for _, id := range row.eventIDs {
				events = append(events, observedEvent{"exit", id, row.reason, true})
			}
			eq(t, rec.observe([]*task{restored}), observed{Events: events, Persists: row.replayed, Remembers: row.replayed}, row.name+": hooks")
		}
	})
}

// candidateRow is an exitCandidate whose notification flag can be absent.
type candidateRow struct {
	id, status string
	notify     bool
	notified   *bool
}

func (c candidateRow) exitState() (string, bool, *bool) { return c.status, c.notify, c.notified }

// scriptedIdentity answers identity probes from a map; a pid with no entry fails the test.
func scriptedIdentity(t *testing.T, answers map[int]*processIdentity) identityProbe {
	return func(pid int) identityReading {
		a, ok := answers[pid]
		if !ok {
			t.Errorf("unexpected identity probe for %d", pid)
			return identityReading{kind: "gone"}
		}
		return reading(a)
	}
}

func failingUnitProbe(t *testing.T) unitActiveProbe {
	return func(string) tri { t.Error("unexpected systemd unit probe"); return triNull }
}

func TestTwinOrphanWatcher(t *testing.T) {
	tw(t, "orphan-eligibility", "orphan eligibility", func(t *testing.T) {
		rows := []struct {
			name     string
			mods     func(*task)
			expected bool
		}{
			{"restored running task with valid pid", func(x *task) { x.Pid = 4242 }, true},
			{"non-restored task", func(x *task) { x.restored = false }, false},
			{"terminal task", func(x *task) { x.Status = "stopped" }, false},
			{"task retains an in-session child handle", func(x *task) { x.child = fakeProc{} }, false},
			{"zero pid", func(x *task) { x.Pid = 0 }, false},
			{"negative pid", func(x *task) { x.Pid = -1 }, false},
		}
		for _, row := range rows {
			eq(t, isOrphanRunning(orphanTask(0, row.mods)), row.expected, row.name)
		}
	})

	tw(t, "orphan-watcher", "orphan watcher poll outcomes", func(t *testing.T) {
		type state struct {
			id, status string
			exit       *int
			notified   bool
			closed     bool
			reason     string
		}
		type poll struct {
			identities map[int]*processIdentity
			finalized  int
			states     []state
			events     [][2]string
			callbacks  [][2]string
			hooks      [4]int
		}
		running := func(id string) state { return state{id, "running", nil, false, false, ""} }
		deadState := func(id string, reason string) state { return state{id, "failed", nil, true, true, reason} }
		recycled := func(pid int) *processIdentity {
			i := *fakeIdent(pid)
			i.StartToken, i.Comm = "start-RECYCLED", "unrelated"
			return &i
		}
		rows := []struct {
			name  string
			tasks []*task
			polls []poll
		}{
			{"matching live identity stays running", []*task{orphanTask(4242, func(x *task) { x.ID = "bg-1" })},
				[]poll{{map[int]*processIdentity{4242: fakeIdent(4242)}, 0, []state{running("bg-1")}, nil, nil, [4]int{}}}},
			{"dead pid finalizes with pid-gone callback and termination annotation", []*task{orphanTask(4242, func(x *task) { x.ID = "bg-1" })},
				[]poll{{map[int]*processIdentity{4242: nil}, 1, []state{deadState("bg-1", "orphaned-pid-gone")}, [][2]string{{"bg-1", "orphaned-pid-gone"}}, [][2]string{{"bg-1", "pid-gone"}}, [4]int{2, 2, 1, 1}}}},
			{"exec changes comm but keeps original pid and start token", []*task{orphanTask(4242, func(x *task) {
				x.ID, x.Command, x.ProcIdent = "bg-bash-exec", "/bin/bash -lc 'sleep 5'", &processIdentity{Pid: 4242, StartToken: "19283746", Comm: "bash"}
			})}, []poll{{map[int]*processIdentity{4242: {Pid: 4242, StartToken: "19283746", Comm: "sleep"}}, 0, []state{running("bg-bash-exec")}, nil, nil, [4]int{}}}},
			{"reused pid finalizes with pid-reused callback and termination annotation", []*task{orphanTask(12345, func(x *task) { x.ID, x.Command = "bg-3", "approval-wait 81" })},
				[]poll{{map[int]*processIdentity{12345: recycled(12345)}, 1, []state{deadState("bg-3", "orphaned-pid-reused")}, [][2]string{{"bg-3", "orphaned-pid-reused"}}, [][2]string{{"bg-3", "pid-reused"}}, [4]int{2, 2, 1, 1}}}},
			{"matching first poll then reused pid", []*task{orphanTask(12345, func(x *task) { x.ID = "bg-3" })}, []poll{
				{map[int]*processIdentity{12345: fakeIdent(12345)}, 0, []state{running("bg-3")}, nil, nil, [4]int{}},
				{map[int]*processIdentity{12345: recycled(12345)}, 1, []state{deadState("bg-3", "orphaned-pid-reused")}, [][2]string{{"bg-3", "orphaned-pid-reused"}}, [][2]string{{"bg-3", "pid-reused"}}, [4]int{2, 2, 1, 1}}}},
			{"matching first poll then dead pid", []*task{orphanTask(4242, func(x *task) { x.ID, x.NotifyOnExit = "bg-3", true })}, []poll{
				{map[int]*processIdentity{4242: fakeIdent(4242)}, 0, []state{running("bg-3")}, nil, nil, [4]int{}},
				{map[int]*processIdentity{4242: nil}, 1, []state{deadState("bg-3", "orphaned-pid-gone")}, [][2]string{{"bg-3", "orphaned-pid-gone"}}, [][2]string{{"bg-3", "pid-gone"}}, [4]int{2, 2, 1, 1}}}},
			{"mixed non-orphan and dead orphan tasks", []*task{
				orphanTask(0, func(x *task) { x.ID, x.restored = "bg-running-child", false }),
				orphanTask(0, func(x *task) { x.ID, x.Status, x.ExitCode = "bg-already-terminal", "completed", exitCode(0) }),
				orphanTask(4242, func(x *task) { x.ID = "bg-orphan-dead" })}, []poll{{map[int]*processIdentity{2409160: nil, 4242: nil}, 1,
				[]state{running("bg-running-child"), {"bg-already-terminal", "completed", exitCode(0), false, false, ""}, deadState("bg-orphan-dead", "orphaned-pid-gone")},
				[][2]string{{"bg-orphan-dead", "orphaned-pid-gone"}}, [][2]string{{"bg-orphan-dead", "pid-gone"}}, [4]int{2, 2, 1, 1}}}},
			{"multiple dead orphans finalize in task order", []*task{orphanTask(1111, func(x *task) { x.ID = "bg-1" }), orphanTask(2222, func(x *task) { x.ID = "bg-2" }), orphanTask(3333, func(x *task) { x.ID = "bg-3" })},
				[]poll{{map[int]*processIdentity{1111: nil, 2222: nil, 3333: nil}, 3, []state{deadState("bg-1", "orphaned-pid-gone"), deadState("bg-2", "orphaned-pid-gone"), deadState("bg-3", "orphaned-pid-gone")},
					[][2]string{{"bg-1", "orphaned-pid-gone"}, {"bg-2", "orphaned-pid-gone"}, {"bg-3", "orphaned-pid-gone"}}, [][2]string{{"bg-1", "pid-gone"}, {"bg-2", "pid-gone"}, {"bg-3", "pid-gone"}}, [4]int{6, 6, 3, 3}}}},
			{"finalized orphan has no effects on subsequent poll", []*task{orphanTask(4242, func(x *task) { x.ID = "bg-1" })}, []poll{
				{map[int]*processIdentity{4242: nil}, 1, []state{deadState("bg-1", "orphaned-pid-gone")}, [][2]string{{"bg-1", "orphaned-pid-gone"}}, [][2]string{{"bg-1", "pid-gone"}}, [4]int{2, 2, 1, 1}},
				{map[int]*processIdentity{4242: nil}, 0, []state{deadState("bg-1", "orphaned-pid-gone")}, [][2]string{{"bg-1", "orphaned-pid-gone"}}, [][2]string{{"bg-1", "pid-gone"}}, [4]int{2, 2, 1, 1}}}},
			{"no recorded identity uses current pid liveness", []*task{orphanTask(4242, func(x *task) { x.ID, x.ProcIdent = "bg-legacy", nil })},
				[]poll{{map[int]*processIdentity{4242: fakeIdent(4242)}, 0, []state{running("bg-legacy")}, nil, nil, [4]int{}}}},
		}
		for _, row := range rows {
			rec, hooks := recordingHooks()
			var identities map[int]*processIdentity
			var callbacks [][2]string
			w := newOrphanWatcher(orphanWatcherDeps{getTasks: func() []*task { return row.tasks }, hooks: hooks, unitActive: failingUnitProbe(t),
				identity: func(pid int) identityReading {
					a, ok := identities[pid]
					if !ok {
						t.Errorf("unexpected identity probe for %d", pid)
					}
					return reading(a)
				},
				timers:     noTimers{t},
				onFinalize: func(x *task, reason string) { callbacks = append(callbacks, [2]string{x.ID, reason}) }})
			for i, p := range row.polls {
				identities = p.identities
				finalized := w.checkOnce()
				obs := rec.observe(row.tasks)
				var states []state
				for _, x := range row.tasks {
					states = append(states, state{x.ID, x.Status, x.ExitCode, x.ExitNotified, x.closed, x.TerminationReason})
				}
				var events [][2]string
				for _, e := range obs.Events {
					events = append(events, [2]string{e.ID, e.Reason})
					eq(t, e.SameTask && e.Typ == "exit", true, row.name+": event is the exit of the very task")
				}
				eq(t, finalized, p.finalized, row.name+": finalized")
				eq(t, states, p.states, row.name+": states")
				eq(t, events, p.events, row.name+": events")
				eq(t, callbacks, p.callbacks, row.name+": callbacks")
				eq(t, [4]int{obs.Persists, obs.Remembers, obs.Refreshes, obs.TimerClears}, p.hooks, row.name+": hooks")
				_ = i
			}
		}
	})

	tw(t, "orphan-watcher-pass", "an orphan pass bounds its probes, joins overlapping passes and yields to stop", func(t *testing.T) {
		rows := []struct {
			name        string
			orphans     int
			during      string
			maxInFlight int
			finalized   []int
			statuses    []string
		}{
			{"six orphans probe at most the concurrency limit at once", 6, "none", probeConcurrency, []int{6}, repeatStrings("failed", 6)},
			{"a pass started while one runs joins it", 2, "second-pass", 2, []int{2, 2}, []string{"failed", "failed"}},
			{"stop during a pass finalizes nothing", 2, "stop", 2, []int{0}, []string{"running", "running"}},
			{"a task finalized while its probe ran is not finalized again", 2, "finalize-first", 2, []int{1}, []string{"completed", "failed"}},
		}
		for _, row := range rows {
			var tasks []*task
			for i := range row.orphans {
				tasks = append(tasks, orphanTask(5000+i, func(x *task) { x.ID = "bg-" + itoa(i+1) }))
			}
			var mu sync.Mutex
			var waiting []chan struct{}
			inFlight, maxInFlight, probes, reads := 0, 0, 0, 0
			_, hooks := recordingHooks()
			w := newOrphanWatcher(orphanWatcherDeps{
				getTasks: func() []*task { mu.Lock(); reads++; mu.Unlock(); return tasks }, hooks: hooks, unitActive: failingUnitProbe(t), timers: noTimers{t},
				identity: func(pid int) identityReading {
					ch := make(chan struct{})
					mu.Lock()
					probes++
					inFlight++
					maxInFlight = max(maxInFlight, inFlight)
					waiting = append(waiting, ch)
					mu.Unlock()
					<-ch
					mu.Lock()
					inFlight--
					mu.Unlock()
					return identityReading{kind: "gone"}
				}})
			results := make(chan int, 2)
			go func() { results <- w.checkOnce() }()
			waitUntil(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(waiting) >= min(row.orphans, probeConcurrency) })
			passes := 1
			if row.during == "second-pass" {
				passes = 2
				go func() { results <- w.checkOnce() }()
				time.Sleep(20 * time.Millisecond)
			}
			if row.during == "stop" {
				w.stop()
			}
			if row.during == "finalize-first" {
				tasks[0].Status, tasks[0].closed = "completed", true
			}
			// Release the probes as they arrive until every pass has returned.
			var got []int
			released := 0
			for len(got) < passes {
				mu.Lock()
				var next chan struct{}
				if released < len(waiting) {
					next = waiting[released]
					released++
				}
				mu.Unlock()
				if next != nil {
					close(next)
				}
				select {
				case n := <-results:
					got = append(got, n)
				case <-time.After(5 * time.Millisecond):
				}
			}
			statuses := []string{}
			for _, x := range tasks {
				statuses = append(statuses, x.Status)
			}
			eq(t, [3]int{maxInFlight, probes, reads}, [3]int{row.maxInFlight, row.orphans, 1}, row.name+": probes")
			eq(t, got, row.finalized, row.name+": finalized")
			eq(t, statuses, row.statuses, row.name+": statuses")
		}
	})

	tw(t, "orphan-watcher-timer", "orphan watcher interval lifecycle", func(t *testing.T) {
		type tstate struct {
			delays, unrefs, cleared, active []int
			reads                           int
			probes                          []int
			eventIDs                        []string
			states                          [][3]any
		}
		type step struct {
			action   string
			handle   int
			expected tstate
		}
		ms := func(n int) int { return n }
		rows := []struct {
			name  string
			poll  time.Duration
			tasks []*task
			steps []step
		}{
			{"start arms an interval and stop clears that handle", 5 * time.Second, nil, []step{
				{"start", 0, tstate{delays: []int{5000}, unrefs: []int{0}, active: []int{0}}},
				{"stop", 0, tstate{delays: []int{5000}, unrefs: []int{0}, cleared: []int{0}, active: []int{}}}}},
			{"second start retains the original timer", 5 * time.Second, nil, []step{
				{"start", 0, tstate{delays: []int{5000}, unrefs: []int{0}, active: []int{0}}},
				{"start", 0, tstate{delays: []int{5000}, unrefs: []int{0}, active: []int{0}}},
				{"stop", 0, tstate{delays: []int{5000}, unrefs: []int{0}, cleared: []int{0}, active: []int{}}}}},
			{"captured default interval polls and restart uses a new handle", 0, []*task{orphanTask(4242, func(x *task) { x.ID = "bg-timer" })}, []step{
				{"start", 0, tstate{delays: []int{30000}, unrefs: []int{0}, active: []int{0}, states: [][3]any{{"bg-timer", "running", false}}}},
				{"fire", 0, tstate{delays: []int{30000}, unrefs: []int{0}, active: []int{0}, reads: 1, probes: []int{4242}, eventIDs: []string{"bg-timer"}, states: [][3]any{{"bg-timer", "failed", true}}}},
				{"stop", 0, tstate{delays: []int{30000}, unrefs: []int{0}, cleared: []int{0}, active: []int{}, reads: 1, probes: []int{4242}, eventIDs: []string{"bg-timer"}, states: [][3]any{{"bg-timer", "failed", true}}}},
				{"start", 0, tstate{delays: []int{30000, 30000}, unrefs: []int{0, 1}, cleared: []int{0}, active: []int{1}, reads: 1, probes: []int{4242}, eventIDs: []string{"bg-timer"}, states: [][3]any{{"bg-timer", "failed", true}}}},
				{"fire", 1, tstate{delays: []int{30000, 30000}, unrefs: []int{0, 1}, cleared: []int{0}, active: []int{1}, reads: 2, probes: []int{4242}, eventIDs: []string{"bg-timer"}, states: [][3]any{{"bg-timer", "failed", true}}}},
				{"stop", 0, tstate{delays: []int{30000, 30000}, unrefs: []int{0, 1}, cleared: []int{0, 1}, active: []int{}, reads: 2, probes: []int{4242}, eventIDs: []string{"bg-timer"}, states: [][3]any{{"bg-timer", "failed", true}}}}}},
		}
		_ = ms
		for _, row := range rows {
			rec, hooks := recordingHooks()
			ft := &fakeTimers{}
			reads := 0
			var probes []int
			w := newOrphanWatcher(orphanWatcherDeps{getTasks: func() []*task { reads++; return row.tasks }, hooks: hooks, poll: row.poll, unitActive: failingUnitProbe(t), timers: ft,
				identity: func(pid int) identityReading { probes = append(probes, pid); return identityReading{kind: "gone"} }})
			for _, s := range row.steps {
				switch s.action {
				case "start":
					w.start()
				case "stop":
					w.stop()
				case "fire":
					if !ft.activeHandle(s.handle) {
						t.Fatalf("%s: requested timer is not active", row.name)
					}
					ft.fire(s.handle)
				}
				var states [][3]any
				for _, x := range row.tasks {
					states = append(states, [3]any{x.ID, x.Status, x.closed})
				}
				ids := []string{}
				for _, e := range rec.observe(row.tasks).Events {
					ids = append(ids, e.ID)
				}
				got := tstate{delays: ft.delaysMs(), unrefs: ft.unrefs(), cleared: ft.clearedIdx(), active: ft.activeIdx(), reads: reads, probes: probes, eventIDs: ids, states: states}
				want := s.expected
				if want.cleared == nil {
					want.cleared = []int{}
				}
				if want.probes == nil {
					want.probes = []int(nil)
				}
				if want.eventIDs == nil {
					want.eventIDs = []string{}
				}
				if got.probes == nil {
					got.probes = []int(nil)
				}
				if want.unrefs == nil {
					want.unrefs = []int{}
				}
				if want.states == nil && got.states == nil {
					want.states = nil
				}
				// Every Go timer is unreferenced by construction (a goroutine never keeps the process alive), so the original's
				// unref calls are the creation order of the handles.
				eq(t, got, want, row.name+": after "+s.action)
			}
			w.stop()
		}
	})

	tw(t, "identity-reading", "an identity read decides restore and the orphan watcher through one liveness verdict", func(t *testing.T) {
		const pid = 4242
		const startToken = "Mon Jan 1 00:00:00 2024"
		type outcome struct {
			reading, restored string
			finalized         int
			watched           string
		}
		unknown := outcome{"unknown", "running", 0, "running"}
		alive := outcome{"identity", "running", 0, "running"}
		gone := outcome{"gone", "stopped", 1, "failed"}
		reused := gone
		reused.reading = "identity"
		rows := []struct {
			name     string
			platform string
			statErr  error
			ps       *probeResult
			pidExist *bool
			want     outcome
		}{
			{"a timed-out ps is unknown and keeps the task running", "", nil, &probeResult{kind: "unsettled", cause: "timed-out"}, nil, unknown},
			{"a signalled ps is unknown and keeps the task running", "", nil, &probeResult{kind: "unsettled", cause: "signalled", signal: "SIGKILL"}, nil, unknown},
			{"a ps that cannot start is unknown and keeps the task running", "", nil, &probeResult{kind: "unsettled", cause: "spawn-failed", code: "EAGAIN"}, nil, unknown},
			{"ps output with too few fields is unknown and keeps the task running", "", nil, &probeResult{kind: "exited", status: 0, stdout: "garbage\n"}, nil, unknown},
			{"a ps that matched no process is gone", "", nil, &probeResult{kind: "exited", status: 1}, nil, gone},
			{"a ps that printed nothing is gone", "", nil, &probeResult{kind: "exited", status: 0, stdout: "\n"}, nil, gone},
			{"a ps naming the recorded start is alive", "", nil, &probeResult{kind: "exited", status: 0, stdout: startToken + " sleep\n"}, nil, alive},
			{"a ps naming another start is a reused pid", "", nil, &probeResult{kind: "exited", status: 0, stdout: "Tue Jan 2 00:00:00 2024 sleep\n"}, nil, reused},
			{"a missing ps and a dead pid are gone", "", nil, &probeResult{kind: "missing"}, ptr(false), gone},
			{"a missing ps and a live pid are alive without an identity", "", nil, &probeResult{kind: "missing"}, ptr(true), outcome{"alive", "running", 0, "running"}},
			{"a Linux pid with no /proc entry is gone without ps", "linux", errNoEnt, nil, nil, gone},
		}
		for _, row := range rows {
			platform := row.platform
			if platform == "" {
				platform = "darwin"
			}
			probe := func(p int) identityReading {
				return defaultReadProcessIdentity(p, identityReaderDeps{platform: platform,
					readStat: func(path string) (string, error) {
						if row.statErr == nil || path != "/proc/4242/stat" {
							t.Errorf("%s: unexpected read %s", row.name, path)
						}
						return "", row.statErr
					},
					run: func(file string, args []string) probeResult {
						if row.ps == nil || file != "ps" || args[len(args)-1] != "4242" {
							t.Errorf("%s: unexpected call %s %v", row.name, file, args)
							return probeResult{}
						}
						return *row.ps
					},
					processAlive: func(p int) bool {
						if row.pidExist == nil || p != pid {
							t.Errorf("%s: unexpected signal check %d", row.name, p)
							return false
						}
						return *row.pidExist
					}})
			}
			procIdent := &processIdentity{Pid: pid, StartToken: startToken, Comm: "bash"}
			r := probe(pid)
			restored := restoredTaskFromSnapshot(fakeSnapshot(func(s *snapshot) { s.Status, s.Pid, s.ProcIdent, s.SessionID = "running", pid, procIdent, "sess-1" }),
				restoreOptions{identity: probe, sessionID: "sess-1", hasSession: true, now: 1_700_000_100_000})
			tk := orphanTask(pid, func(x *task) { x.ProcIdent = procIdent })
			_, hooks := recordingHooks()
			w := newOrphanWatcher(orphanWatcherDeps{getTasks: func() []*task { return []*task{tk} }, hooks: hooks, identity: probe, unitActive: failingUnitProbe(t), timers: noTimers{t}})
			finalized := w.checkOnce()
			eq(t, outcome{r.kind, restored.Status, finalized, tk.Status}, row.want, row.name)
		}
	})

	tw(t, "resource-unit-lifecycle", "systemd unit state takes precedence over wrapper identity during restore and polling", func(t *testing.T) {
		const unit = "kendex-pi-bg-bg-7.service"
		identity := &processIdentity{Comm: "systemd-run", Pid: 4242, StartToken: "start-4242"}
		type result struct {
			status               string
			closed, exitNotified bool
			reason               string
			finalized            *int
		}
		rows := []struct {
			name      string
			operation string
			active    bool
			identity  *processIdentity
			want      result
		}{
			{"active unit keeps restored task running despite missing wrapper", "restore", true, nil, result{"running", false, false, "", nil}},
			{"inactive unit stops restored task despite live wrapper", "restore", false, identity, result{"stopped", true, false, "reconcile-on-restart", nil}},
			{"watcher leaves active unit running before probing wrapper", "watch", true, nil, result{"running", false, false, "", ptr(0)}},
		}
		for _, row := range rows {
			s := fakeSnapshot(func(x *snapshot) {
				x.Command, x.Cwd, x.ID, x.LogFile, x.OutputBytes, x.Pid, x.ProcIdent, x.SessionID, x.Title = "sleep 60", "/tmp/work", "bg-rc", "/tmp/bg-rc.log", 0, 4242, identity, "session-A", "sleep 60"
				x.ResourceControl = &resourceControlMeta{Mode: "systemd-run", RequestedMode: "auto", UnitName: unit}
			})
			var unitCalls []string
			var identityCalls []int
			unitProbe := func(name string) tri {
				unitCalls = append(unitCalls, name)
				if row.active {
					return triTrue
				}
				return triFalse
			}
			idProbe := func(pid int) identityReading {
				identityCalls = append(identityCalls, pid)
				return reading(row.identity)
			}
			rec, hooks := recordingHooks()
			var tk *task
			var finalized *int
			if row.operation == "restore" {
				tk = restoredTaskFromSnapshot(s, restoreOptions{sessionID: "session-A", hasSession: true, identity: idProbe, unitActive: unitProbe, now: 1_700_000_001_000})
			} else {
				tk = fakeTask(func(x *task) { x.snapshot = s; x.restored = true; x.NotifyMode = "transition" })
				w := newOrphanWatcher(orphanWatcherDeps{getTasks: func() []*task { return []*task{tk} }, hooks: hooks, identity: idProbe, unitActive: unitProbe, timers: noTimers{t}})
				n := w.checkOnce()
				finalized = &n
			}
			eq(t, result{tk.Status, tk.closed, tk.ExitNotified, tk.TerminationReason, finalized}, row.want, row.name)
			eq(t, unitCalls, []string{unit}, row.name+": unit calls")
			eq(t, identityCalls, []int(nil), row.name+": identity calls")
			eq(t, rec.observe([]*task{tk}), observed{Events: []observedEvent{}}, row.name+": hooks")
		}
	})
}

func repeatStrings(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(time.Millisecond)
	}
}

type fakeProc struct{}

func (fakeProc) Pid() int { return 1 }

var errNoEnt = errNotExist{}

type errNotExist struct{}

func (errNotExist) Error() string   { return "no such file" }
func (errNotExist) Is(t error) bool { return t == fs.ErrNotExist }

// noTimers fails a test that arms a timer it did not expect.
type noTimers struct{ t *testing.T }

func (n noTimers) After(time.Duration, func()) timer {
	n.t.Error("unexpected timer")
	return nopTimer{}
}
func (n noTimers) Every(time.Duration, func()) timer {
	n.t.Error("unexpected interval")
	return nopTimer{}
}

type nopTimer struct{}

func (nopTimer) Stop() {}

// fakeTimers records the timers armed and fires them by hand. Timers are numbered in creation order.
type fakeTimers struct {
	mu      sync.Mutex
	handles []*fakeHandle
}

type fakeHandle struct {
	f       func()
	d       time.Duration
	kind    string
	stopped bool
	fired   bool
}

type fakeTimer struct {
	owner *fakeTimers
	h     *fakeHandle
}

func (f fakeTimer) Stop() {
	f.owner.mu.Lock()
	f.h.stopped = true
	f.owner.mu.Unlock()
}

func (f *fakeTimers) add(kind string, d time.Duration, fn func()) timer {
	f.mu.Lock()
	defer f.mu.Unlock()
	h := &fakeHandle{f: fn, d: d, kind: kind}
	f.handles = append(f.handles, h)
	return fakeTimer{f, h}
}

func (f *fakeTimers) After(d time.Duration, fn func()) timer { return f.add("timeout", d, fn) }
func (f *fakeTimers) Every(d time.Duration, fn func()) timer { return f.add("interval", d, fn) }

func (f *fakeTimers) activeHandle(i int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return i < len(f.handles) && !f.handles[i].stopped && !f.handles[i].fired
}

// fire runs a timer's callback; a timeout is consumed.
func (f *fakeTimers) fire(i int) {
	f.mu.Lock()
	h := f.handles[i]
	if h.kind == "timeout" {
		h.fired = true
	}
	f.mu.Unlock()
	h.f()
}

func (f *fakeTimers) delaysMs() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []int{}
	for _, h := range f.handles {
		out = append(out, int(h.d/time.Millisecond))
	}
	return out
}

func (f *fakeTimers) unrefs() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []int{}
	for i := range f.handles {
		out = append(out, i)
	}
	return out
}

func (f *fakeTimers) clearedIdx() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []int{}
	for i, h := range f.handles {
		if h.stopped {
			out = append(out, i)
		}
	}
	return out
}

func (f *fakeTimers) activeIdx() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []int{}
	for i, h := range f.handles {
		if !h.stopped && !h.fired {
			out = append(out, i)
		}
	}
	return out
}

var _ = strings.Join
