package pi_subagents

import (
	"path/filepath"
	"strings"
	"sync"
)

// The runner side of steering for a background run of this port (the original's runner: registerSteer of run-child-session.ts and
// deliverSteerRequest, applySteerDelivery and the shutdown sweep of subagent-runner.ts). The supervisor of a run calls steerConsumer
// once its child is up; the consumer watches the run's steer inbox, hands each request to the child session (`steer` or `follow_up`
// over the rpc child), and keeps the status file's steering ledger: scheduled/routed when it takes the request, queued when the child
// accepted it, delivered when the child's own `message_end` of that user text shows it was consumed, failed otherwise.

func init() { steerConsumer = consumeSteerRequestsForRun }

// steerDelivery is the outcome of handing a request to the child session (the original's SteerDelivery).
type steerDelivery struct {
	State   string // "delivered", "queued" or "failed"
	Message string
}

func formatSteerMessage(r steerRequest) string {
	head := "Mid-run steering from the parent orchestrator:"
	if r.Mode == steerModeFollowUp {
		head = "Queued follow-up from the parent orchestrator:"
	}
	return strings.Join([]string{head, "", r.Message, "", "Incorporate this guidance at the next safe point. Do not restart the task unless the guidance explicitly asks you to."}, "\n")
}

type acceptedSteer struct {
	request steerRequest
	text    string
}

// steerRouter hands requests to one child and remembers the accepted ones until the child's own user message shows each consumed.
type steerRouter struct {
	child     childSession
	mu        sync.Mutex
	accepted  []acceptedSteer
	onOutcome func(r steerRequest, d steerDelivery)
}

func newSteerRouter(child childSession, onOutcome func(steerRequest, steerDelivery)) *steerRouter {
	return &steerRouter{child: child, onOutcome: onOutcome}
}

// steer sends the request: "queued" when the child accepted it, "failed" with the transport's message when it did not (and then
// no outcome follows).
func (s *steerRouter) steer(r steerRequest) steerDelivery {
	text := formatSteerMessage(r)
	follow := r.Mode == steerModeFollowUp
	entry := acceptedSteer{r, text}
	s.mu.Lock()
	s.accepted = append(s.accepted, entry)
	s.mu.Unlock()
	var err error
	if follow {
		err = s.child.FollowUp(text)
	} else {
		err = s.child.Steer(text)
	}
	if err != nil {
		s.mu.Lock()
		for i, a := range s.accepted {
			if a.request.ID == entry.request.ID && a.text == entry.text {
				s.accepted = append(s.accepted[:i], s.accepted[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
		return steerDelivery{"failed", err.Error()}
	}
	if follow {
		return steerDelivery{"queued", "Pi queued the follow-up input."}
	}
	return steerDelivery{"queued", "Pi accepted the steering input."}
}

// observe is the child's event: its user `message_end` of an accepted text consumes the oldest accepted request of that text.
func (s *steerRouter) observe(ev childEvent) {
	if ev["type"] != "message_end" {
		return
	}
	msg, _ := ev["message"].(map[string]any)
	if msg == nil || msg["role"] != "user" {
		return
	}
	text := textOfMessage(msg)
	if text == "" {
		return
	}
	s.mu.Lock()
	m, rest, ok := takeMatchingAcceptedSteer(s.accepted, func(a acceptedSteer) string { return a.text }, text)
	if ok {
		s.accepted = rest
	}
	s.mu.Unlock()
	if ok && s.onOutcome != nil {
		s.onOutcome(m.request, steerDelivery{"delivered", "Child consumed the steering input."})
	}
}

// failUnconsumed settles every accepted request the child never consumed.
func (s *steerRouter) failUnconsumed() {
	s.mu.Lock()
	rest := s.accepted
	s.accepted = nil
	s.mu.Unlock()
	for _, a := range rest {
		if s.onOutcome != nil {
			s.onOutcome(a.request, steerDelivery{"failed", unconsumedSteerReason(a.request.Mode)})
		}
	}
}

// ---- the runner's ledger ----

type steerRunner struct {
	cfg    *RunConfig
	router *steerRouter

	mu      sync.Mutex
	pending []steerRequest // requests for a step that has not started
	wg      sync.WaitGroup // outcomes being applied
}

func (r *steerRunner) emit(typ string, req steerRequest, index *int, extra map[string]any) {
	ev := map[string]any{"type": typ, "ts": nowMs(), "runId": r.cfg.ID, "requestId": req.ID}
	if index != nil {
		ev["index"] = *index
	}
	for k, v := range extra {
		ev[k] = v
	}
	appendJSONLine(filepath.Join(r.cfg.AsyncDir, "events.jsonl"), ev)
}

func (r *steerRunner) emitNotice(requestID, state, message string) {
	n := map[string]any{"type": "subagent.steering.notice", "ts": nowMs(), "runId": r.cfg.ID, "requestId": requestID, "state": state, "message": message}
	if r.cfg.SessionID != "" {
		n["currentSessionId"] = r.cfg.SessionID
	}
	appendJSONLine(filepath.Join(r.cfg.AsyncDir, "events.jsonl"), n)
}

func steeringMarkerPath(asyncDir, requestID string) string {
	return filepath.Join(steerRecoveryDir(asyncDir), base64URL(requestID)+".json")
}

type steerTargetPlan struct {
	Index  int    `json:"index"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// recordLifecycle records a request in the ledger of the status and of each of its steps.
func recordSteeringLifecycle(st *AsyncStatus, req steerRequest, targets []steerTargetPlan) {
	l := statusSteering(st)
	if l == nil {
		l = createSteeringStatus()
	}
	ts := make([]steeringTarget, len(targets))
	for i, t := range targets {
		ts[i] = steeringTarget{Index: t.Index, State: t.State, Reason: t.Reason}
	}
	recordSteeringRequest(l, steerRequestInput{ID: req.ID, RequestedAt: int64(req.TS), Source: req.Source, Message: req.Message, Targets: ts})
	setStatusSteering(st, l)
	for i, t := range targets {
		if t.Index < 0 || t.Index >= len(st.Steps) {
			continue
		}
		ss := stepSteering(&st.Steps[t.Index])
		if ss == nil {
			ss = createSteeringStatus()
		}
		recordSteeringRequest(ss, steerRequestInput{ID: req.ID, RequestedAt: int64(req.TS), Source: req.Source, Message: req.Message, Targets: []steeringTarget{ts[i]}})
		setStepSteering(&st.Steps[t.Index], ss)
	}
}

// updateSteeringLifecycleTarget moves a target in the ledger of the status and of its step.
func updateSteeringLifecycleTarget(st *AsyncStatus, requestID string, index int, state string, now int64, fields steeringTargetFields) {
	if l := statusSteering(st); l != nil {
		updateSteeringTarget(l, requestID, index, state, now, fields)
		setStatusSteering(st, l)
	}
	if index >= 0 && index < len(st.Steps) {
		if ss := stepSteering(&st.Steps[index]); ss != nil {
			updateSteeringTarget(ss, requestID, index, state, now, fields)
			setStepSteering(&st.Steps[index], ss)
		}
	}
}

func markSteeringAttention(st *AsyncStatus, index int) {
	if index >= 0 && index < len(st.Steps) {
		st.Steps[index].ActivityState = "needs_attention"
	}
	st.ActivityState = "needs_attention"
}

// mutate reads the status, applies fn and writes it back under the process's status lock; it reports whether a status existed.
func (r *steerRunner) mutate(fn func(st *AsyncStatus)) bool {
	statusMu.Lock()
	defer statusMu.Unlock()
	st, err := readStatus(r.cfg.AsyncDir)
	if err != nil || st == nil {
		return false
	}
	fn(st)
	st.LastUpdate = msPtrOf(nowMs())
	_ = writeStatus(r.cfg.AsyncDir, st)
	return true
}

func (r *steerRunner) terminalNotice(st *AsyncStatus, requestID, failureMessage string) {
	l := statusSteering(st)
	if l == nil {
		return
	}
	switch terminalSteeringNoticeState(l, requestID) {
	case "partial":
		r.emitNotice(requestID, "partial", "Steering partially delivered for run "+r.cfg.ID+".")
	case "failed":
		r.emitNotice(requestID, "failed", failureMessage)
	}
}

// deliver takes one request: it records where each target stands and hands the routed ones to the child.
func (r *steerRunner) deliver(req steerRequest) {
	var routed []int
	r.mutate(func(st *AsyncStatus) {
		if st.State != "running" {
			reason := "run became " + st.State + " before steering request was consumed"
			var indexes []int
			switch {
			case req.TargetIndex != nil:
				indexes = []int{*req.TargetIndex}
			case len(req.TargetIndexes) > 0:
				indexes = req.TargetIndexes
			default:
				for i := range st.Steps {
					indexes = append(indexes, i)
				}
			}
			targets := make([]steerTargetPlan, len(indexes))
			for i, ix := range indexes {
				targets[i] = steerTargetPlan{ix, "failed", reason}
			}
			recordSteeringLifecycle(st, req, targets)
			r.emit("subagent.steer.requested", req, nil, map[string]any{"targets": targets})
			for _, t := range targets {
				ix := t.Index
				r.emit("subagent.steer.failed", req, &ix, map[string]any{"reason": reason})
			}
			r.terminalNotice(st, req.ID, "Steering failed for run "+r.cfg.ID+": "+reason+".")
			return
		}
		running := runningIndexes(st.Steps)
		var targetIdx []int
		switch {
		case req.TargetIndex != nil:
			targetIdx = []int{*req.TargetIndex}
		case len(req.TargetIndexes) > 0:
			targetIdx = req.TargetIndexes
		case len(running) > 0:
			targetIdx = running
		case st.Mode == "single" && len(st.Steps) > 0 && st.Steps[0].Status == "pending":
			targetIdx = []int{0}
		}
		now := nowMs()
		plans := make([]steerTargetPlan, len(targetIdx))
		for i, ix := range targetIdx {
			switch {
			case ix < 0 || ix >= len(st.Steps):
				plans[i] = steerTargetPlan{ix, "failed", "child index out of range"}
			case st.Steps[ix].Status == "pending":
				plans[i] = steerTargetPlan{Index: ix, State: "scheduled"}
			case st.Steps[ix].Status != "running":
				plans[i] = steerTargetPlan{ix, "failed", "child is " + st.Steps[ix].Status}
			default:
				plans[i] = steerTargetPlan{Index: ix, State: "routed"}
			}
		}
		recordSteeringLifecycle(st, req, plans)
		r.emit("subagent.steer.requested", req, nil, map[string]any{"targets": plans})
		for _, p := range plans {
			ix := p.Index
			switch p.State {
			case "routed":
				updateSteeringLifecycleTarget(st, req.ID, ix, "routed", now, steeringTargetFields{})
				r.emit("subagent.steer.routed", req, &ix, nil)
				routed = append(routed, ix)
			case "failed":
				markSteeringAttention(st, ix)
				r.emit("subagent.steer.failed", req, &ix, map[string]any{"reason": p.Reason})
			default:
				r.emit("subagent.steer.scheduled", req, &ix, nil)
			}
		}
		r.terminalNotice(st, req.ID, "Steering failed for run "+r.cfg.ID+": no requested child remained steerable.")
	})
	for _, ix := range routed {
		// Outside the status lock: the child's reply is read by the goroutine that also reports its events.
		d := r.router.steer(req)
		r.applyDelivery(req.ID, ix, d)
	}
}

// applyDelivery records the outcome of handing a routed steer to the child (the original's applySteerDelivery).
func (r *steerRunner) applyDelivery(requestID string, index int, d steerDelivery) {
	r.mutate(func(st *AsyncStatus) {
		l := statusSteering(st)
		if l == nil {
			return
		}
		req := findSteeringRequest(l, requestID)
		if req == nil {
			return
		}
		var target *steeringTarget
		for i := range req.Targets {
			if req.Targets[i].Index == index {
				target = &req.Targets[i]
			}
		}
		if target == nil || target.State == "delivered" || target.State == "late" || target.State == "failed" || target.State == d.State {
			return
		}
		late := pathExists(steeringMarkerPath(r.cfg.AsyncDir, requestID))
		now := nowMs()
		synth := steerRequest{Type: "steer", ID: requestID, TS: float64(now), Message: d.Message}
		ix := index
		switch d.State {
		case "delivered":
			state, fields := "delivered", steeringTargetFields{}
			if late {
				state, fields = "late", steeringTargetFields{Reason: "acknowledged after recovery commit"}
			}
			updateSteeringLifecycleTarget(st, requestID, index, state, now, fields)
			r.emit("subagent.steer.delivered", synth, &ix, map[string]any{"late": late, "deliveryStatus": "delivered", "message": d.Message})
		case "queued":
			updateSteeringLifecycleTarget(st, requestID, index, "queued", now, steeringTargetFields{})
			r.emit("subagent.steer.queued", synth, &ix, map[string]any{"deliveryStatus": "queued", "message": d.Message})
		default:
			markSteeringAttention(st, index)
			updateSteeringLifecycleTarget(st, requestID, index, "failed", now, steeringTargetFields{Reason: d.Message})
			r.emit("subagent.steer.failed", synth, &ix, map[string]any{"reason": d.Message})
		}
		r.terminalNotice(st, requestID, "Steering failed for run "+r.cfg.ID+": "+d.Message)
	})
}

// onSteer is the inbox callback: a request for a step that has not started waits (and is delivered once more when the step runs).
func (r *steerRunner) onSteer(req steerRequest) {
	st, _ := readStatus(r.cfg.AsyncDir)
	pending := false
	if st != nil {
		switch {
		case req.TargetIndex != nil:
			pending = *req.TargetIndex < len(st.Steps) && st.Steps[*req.TargetIndex].Status == "pending"
		case req.TargetIndexes != nil:
		default:
			pending = len(runningIndexes(st.Steps)) == 0
		}
	}
	r.deliver(req)
	if pending {
		r.mu.Lock()
		r.pending = append(r.pending, req)
		r.mu.Unlock()
	}
}

// flushPending delivers the waiting requests once their step runs.
func (r *steerRunner) flushPending() {
	r.mu.Lock()
	if len(r.pending) == 0 {
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	st, _ := readStatus(r.cfg.AsyncDir)
	if st == nil || len(st.Steps) == 0 || st.Steps[0].Status != "running" {
		return
	}
	r.mu.Lock()
	waiting := r.pending
	r.pending = nil
	r.mu.Unlock()
	for _, req := range waiting {
		if req.TargetIndex == nil {
			req.TargetIndex = new(0)
		}
		if *req.TargetIndex == 0 {
			r.deliver(req)
		} else {
			r.mu.Lock()
			r.pending = append(r.pending, req)
			r.mu.Unlock()
		}
	}
}

// consumeSteerRequestsForRun starts the steering of one run and returns the function that ends it. The supervisor calls that
// function once the run's terminal status is written: it closes the inbox, fails what is still queued and settles the ledger.
func consumeSteerRequestsForRun(cfg *RunConfig, child childSession) (stop func()) {
	r := &steerRunner{cfg: cfg}
	r.router = newSteerRouter(child, func(req steerRequest, d steerDelivery) {
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			r.applyDelivery(req.ID, 0, d)
		}()
	})
	off := child.Subscribe(r.router.observe)
	disposeWatch := watchAsyncControlInbox(cfg.AsyncDir, controlWatch{OnSteer: r.onSteer})
	stopFlush := realControlTimers{}.Every(controlInboxPollPeriod, r.flushPending)
	return func() {
		disposeWatch()
		stopFlush()
		off()
		r.router.failUnconsumed()
		r.wg.Wait()
		r.shutdown()
	}
}

// shutdown closes the steer inbox with the run's final state, fails the requests that arrived too late and the ledger targets
// no child consumed (the sweep at the end of the original's runner).
func (r *steerRunner) shutdown() {
	state := "complete"
	if st, _ := readStatus(r.cfg.AsyncDir); st != nil {
		state = st.State
	}
	_ = closeSteerInbox(r.cfg.AsyncDir, state)
	for _, req := range consumeSteerRequests(r.cfg.AsyncDir, nil, nil) {
		r.deliver(req)
	}
	type failedTarget struct {
		id    string
		index int
	}
	var failed []failedTarget
	r.mutate(func(st *AsyncStatus) {
		l := statusSteering(st)
		if l == nil {
			return
		}
		for _, req := range l.Recent {
			changed := false
			for _, t := range req.Targets {
				if t.State != "scheduled" && t.State != "routed" && t.State != "queued" {
					continue
				}
				changed = true
				reason := "child terminated before steering delivery"
				if t.State == "queued" {
					reason = unconsumedSteerReason("")
				}
				updateSteeringLifecycleTarget(st, req.ID, t.Index, "failed", nowMs(), steeringTargetFields{Reason: reason})
				ix := t.Index
				r.emit("subagent.steer.failed", steerRequest{ID: req.ID}, &ix, map[string]any{"reason": reason})
			}
			if changed {
				failed = append(failed, failedTarget{id: req.ID})
			}
		}
		for _, f := range failed {
			r.terminalNotice(st, f.id, "Steering failed for run "+r.cfg.ID+": child terminated before delivery.")
		}
	})
}
