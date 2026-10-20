package pi_subagents

// Port of src/runs/shared/readonly-drain-observation.ts: private, installation-owned evidence that a read-only child's ordinary
// indexed drain found no work and finished normally. It never changes what the drain or its queries do and keeps no status. It
// observes the ordinary drain, not historical ownership or outside work that appears later.

var knownDrainStates = map[string]bool{"queued": true, "running": true, "complete": true, "failed": true, "partial": true, "paused": true, "stopped": true, "rejected": true}

// ReadonlyDrainObservation is the evidence of one drain of the session whose session file is file; guard must report whether
// nothing else (a background work provider) could hold work the ordinary drain does not see.
type ReadonlyDrainObservation struct {
	state   string // "pending", "empty" or "denied"
	started bool
	first   bool
	file    string
	guard   func() bool
}

func newReadonlyDrainObservation(file string, guard func() bool) *ReadonlyDrainObservation {
	return &ReadonlyDrainObservation{state: "pending", file: file, guard: guard}
}

func (o *ReadonlyDrainObservation) deny() {
	if o != nil {
		o.state = "denied"
	}
}

// check runs the guard; a false or panicking guard denies, and the evidence is intact while it is not denied.
func (o *ReadonlyDrainObservation) check() (ok bool) {
	if o == nil {
		return true
	}
	defer func() {
		if recover() != nil {
			o.deny()
			ok = o.state != "denied"
		}
	}()
	if !o.guard() {
		o.deny()
	}
	return o.state != "denied"
}

// begin starts the drain: only one drain of the exact session file, run by the installed runtime (native: no injected
// dependency), can count.
func (o *ReadonlyDrainObservation) begin(file string, native bool) {
	if o == nil {
		return
	}
	if o.started || file != o.file || !native {
		o.deny()
	}
	o.started = true
	o.check()
}

// status is called with each raw status the drain's first read sees, before any repair or filtering; nil means a query met
// uncertainty (a missing, unreadable or invalid status).
func (o *ReadonlyDrainObservation) status(st *AsyncStatus) {
	if o == nil {
		return
	}
	switch {
	case st == nil || st.SessionID == "" || !knownDrainStates[st.State]:
		o.deny()
	case st.SessionID == o.file && (st.State == "queued" || st.State == "running"):
		o.deny()
	}
}

// predicate records the first work query of the drain; work at first sight denies for good.
func (o *ReadonlyDrainObservation) predicate(hasWork bool) {
	if o == nil || o.first {
		return
	}
	o.first = true
	if hasWork {
		o.deny()
	}
}

// complete marks the drain finished: empty only if it started, queried once and the guard still holds.
func (o *ReadonlyDrainObservation) complete() {
	if o != nil && o.started && o.first && o.check() {
		o.state = "empty"
	}
}

// settled reports that the ordinary drain found nothing, completed, and nothing holds work the drain does not see.
func (o *ReadonlyDrainObservation) settled() bool { return o != nil && o.check() && o.state == "empty" }
