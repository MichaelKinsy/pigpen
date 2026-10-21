package pi_background_tasks

import (
	"strings"
	"testing"
)

// Fixtures of the original's tests (tests/fixtures/lifecycle.ts, wake.ts, orphan-watcher.ts, resource-control.ts,
// bounded-snapshot.ts), with the same defaults.

func fakeIdent(pid int) *processIdentity {
	return &processIdentity{Pid: pid, StartToken: "start-" + itoa(pid), Comm: "approval-wait"}
}

// reading is an identity probe answer: nil reads as a pid the kernel reports gone.
func reading(identity *processIdentity) identityReading {
	if identity != nil {
		return identityReading{kind: "identity", identity: *identity}
	}
	return identityReading{kind: "gone"}
}

func fakeSnapshot(mods ...func(*snapshot)) snapshot {
	s := snapshot{
		Command: "approval-wait 81", Cwd: "/private/worktree", ID: "bg-3", LogFile: "/private/log.txt", NotifyOnExit: true,
		Pid: 2409160, SessionID: "sess-1", StartedAt: 1_700_000_000_000, Status: statusRunning, Title: "bot review wait PR 81", UpdatedAt: 1_700_000_000_000,
	}
	for _, m := range mods {
		m(&s)
	}
	return s
}

func fakeTask(mods ...func(*task)) *task {
	t := &task{snapshot: fakeSnapshot()}
	for _, m := range mods {
		m(t)
	}
	return t
}

// orphanTask is the original's orphanTask: pid defaults to 2409160 and carries its own identity; mods win over the defaults.
func orphanTask(pid int, mods ...func(*task)) *task {
	if pid == 0 {
		pid = 2409160
	}
	t := fakeTask()
	t.Pid = pid
	t.ProcIdent = fakeIdent(pid)
	t.restored = true
	for _, m := range mods {
		m(t)
	}
	return t
}

func exitCode(n int) *int { return &n }

// hookRecord is the original's recordingHooks: hooks that count and record, never performing host effects.
type hookRecord struct {
	events                                      []recordedEvent
	persists, remembers, refreshes, timerClears int
	sendReturns                                 bool
}

type recordedEvent struct {
	typ    string
	task   *task
	id     string
	reason string
}

func recordingHooks(sendReturns ...bool) (*hookRecord, lifecycleHooks) {
	r := &hookRecord{sendReturns: true}
	if len(sendReturns) > 0 {
		r.sendReturns = sendReturns[0]
	}
	return r, lifecycleHooks{
		rememberSnapshot: func(t *task) *snapshot { r.remembers++; s := *taskSnapshot(t); return &s },
		persistSnapshots: func() { r.persists++ },
		sendTaskEvent: func(typ string, t *task) bool {
			r.events = append(r.events, recordedEvent{typ, t, t.ID, t.TerminationReason})
			return r.sendReturns
		},
		refreshUI:       func() { r.refreshes++ },
		clearTaskTimers: func(*task) { r.timerClears++ },
	}
}

// observed is observe(tasks): the events with whether each names the very task of the list, and the counters.
type observed struct {
	Events                                      []observedEvent
	Persists, Remembers, Refreshes, TimerClears int
}

type observedEvent struct {
	Typ      string
	ID       string
	Reason   string
	SameTask bool
}

func (r *hookRecord) observe(tasks []*task) observed {
	out := observed{Events: []observedEvent{}, Persists: r.persists, Remembers: r.remembers, Refreshes: r.refreshes, TimerClears: r.timerClears}
	for _, e := range r.events {
		same := false
		for _, t := range tasks {
			if t.ID == e.id {
				same = t == e.task
				break
			}
		}
		out.Events = append(out.Events, observedEvent{e.typ, e.id, e.reason, same})
	}
	return out
}

// repeat is "x".repeat(n).
func repeat(s string, n int) string { return strings.Repeat(s, n) }

// sendRecord is one message the fixture's sendMessage received.
type sendRecord struct {
	message customMessage
	options sendOptions
}

// sendDeps is tests/fixtures/wake.ts sendDeps: a clock fixed at 2000, an output tail of the given text.
func sendDeps(output string, diagnostics *[]wakeDiagnostic) (sendTaskWakeDeps, *[]sendRecord) {
	var messages []sendRecord
	deps := sendTaskWakeDeps{
		isShuttingDown:   func() bool { return false },
		logDiagnostic:    func(d wakeDiagnostic) { *diagnostics = append(*diagnostics, d) },
		messageType:      "pi-bg-task",
		now:              func() int64 { return 2000 },
		outputTail:       func(*task) string { return tailText(output, defaultOutputAlertMaxChars, false) },
		rememberSnapshot: func(t *task) *snapshot { return taskSnapshot(t) },
		sendMessage:      func(m customMessage, o sendOptions) { messages = append(messages, sendRecord{m, o}) },
	}
	return deps, &messages
}

func noDiagnostics() *[]wakeDiagnostic { return &[]wakeDiagnostic{} }

func mustTrue(t *testing.T, cond bool, what string) {
	t.Helper()
	if !cond {
		t.Error(what)
	}
}
