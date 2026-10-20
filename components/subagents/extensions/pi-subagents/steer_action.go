package pi_subagents

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Port of src/runs/foreground/async-steering-action.ts: `subagent {action: "steer"}` for a background run. The steer is a request
// file in the run's steer inbox; the run's runner (the supervisor of this port, or the original's runner) delivers it to the child
// and records the outcome in the status file's steering ledger, which this action waits for. A steer that no runner acknowledges
// is not reported as delivered; a single-child run whose runner is gone is paused and revived with the steer as its task.

// steerReply is the result of a steer action: its text, whether it is an error and details.steering.
type steerReply struct {
	Text     string
	IsError  bool
	Steering *steerActionResult
	// AsyncID is the replacement run of a recovered steer's launch (details.asyncId of the recover callback).
}

// steerRecoverResult is what the recover callback (a background resume launch) reports.
type steerRecoverResult struct {
	AsyncID string
	// Error is the launch's error text; an empty AsyncID with no Error is a failed launch without a message.
	Error string
}

// steerRunInput is the input of steerAsyncRun (the original's object parameter).
type steerRunInput struct {
	CurrentSessionID string
	RunID            string
	Message          string
	Mode             steerDeliveryMode
	Index            *int
	// AsyncDir is the run's live directory; "" when the run has none.
	AsyncDir   string
	Dirs       asyncDirs
	Kill       killFunc
	ResultsDir string
	// FindPendingAsks lists the pending supervisor asks of a child (nil: not checked).
	FindPendingAsks func(runID, agent string, childIndex int) []string
	// Cancelled reports that the caller aborted (the original's AbortSignal).
	Cancelled func() bool
	// AckTimeoutMs and RecoveryTimeoutMs default to 3 s and 15 s.
	AckTimeoutMs      *int64
	RecoveryTimeoutMs *int64
	// Touch tells the session's tracker that the run was just addressed.
	Touch                 func()
	OnRequestQueued       func(requestPath string)
	OnBeforeRecoveryClaim func(requestID string, committedAt int64)
	OnRecoveryCommitted   func(requestID string, committedAt int64)
	Recover               func(limits steeringRecoveryLimits) steerRecoverResult
}

func steerFail(text string) steerReply { return steerReply{Text: text, IsError: true} }

func steerTargetsAccepted(r *steerActionResult) bool {
	return r != nil && len(r.Targets) > 0 && allTargets(r.Targets, func(s string) bool { return s == "queued" || s == "delivered" })
}

func statusExtraString(st *AsyncStatus, key string) string {
	var s string
	if raw, ok := st.Extra[key]; ok && json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

func statusExtraBool(st *AsyncStatus, key string) bool {
	var b bool
	if raw, ok := st.Extra[key]; ok && json.Unmarshal(raw, &b) == nil {
		return b
	}
	return false
}

// canQueueRetainedAsyncFollowUp is whether a completed workflow-owned single child keeps a session a follow-up can wait for.
func canQueueRetainedAsyncFollowUp(st *AsyncStatus, index *int) bool {
	if st.State != "complete" || statusExtraString(st, "parentWorkflowRunId") == "" || len(st.Steps) != 1 {
		return false
	}
	step := st.Steps[0]
	if step.Status != "complete" && step.Status != "completed" {
		return false
	}
	if firstNonEmpty(step.SessionFile, st.SessionFile) == "" {
		return false
	}
	return index == nil || *index == 0
}

func stepRunning(st string) bool  { return st == "running" }
func stepRunnable(st string) bool { return st == "running" || st == "pending" }

func runningIndexes(steps []AsyncStep) []int {
	var out []int
	for i, s := range steps {
		if s.Status == "running" {
			out = append(out, i)
		}
	}
	return out
}

// waitForSteeringAction polls the status file for the outcome of a request until it is terminal or the timeout ends; nil when the
// run has no result for it. A cancelled wait returns nil at once.
func waitForSteeringAction(asyncDir, sourceRunID, requestID string, timeoutMs int64, cancelled func() bool) *steerActionResult {
	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	read := func() *steerActionResult {
		if s := readSteeringStatus(asyncDir); s != nil {
			return actionResultFromSteeringStatus(s, sourceRunID, requestID, "")
		}
		return nil
	}
	for !time.Now().After(deadline) {
		if cancelled != nil && cancelled() {
			return nil
		}
		if r := read(); steeringActionIsTerminal(r) {
			return r
		}
		wait := time.Until(deadline)
		if wait > 50*time.Millisecond {
			wait = 50 * time.Millisecond
		}
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		time.Sleep(wait)
	}
	return read()
}

func steeringNoticeLine(asyncDir string, notice steeringNotice) {
	b, err := json.Marshal(notice)
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(asyncDir, "events.jsonl"), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return // the action result and status stay authoritative if the diagnostic notice cannot be persisted
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

func supervisorReplyCommands(replyTos []string) string {
	lines := make([]string, len(replyTos))
	for i, r := range replyTos {
		s, _ := marshalNoEscape(struct {
			Action  string `json:"action"`
			ReplyTo string `json:"replyTo"`
			Message string `json:"message"`
		}{"reply", r, "<explicit answer>"})
		lines[i] = "subagent_supervisor(" + s + ")"
	}
	return strings.Join(lines, "\n")
}

// steerAsyncRun is the steer action of a background run.
func steerAsyncRun(in steerRunInput) steerReply {
	if in.AsyncDir == "" {
		return steerFail(fmt.Sprintf("Async run '%s' has no live run directory to steer.", in.RunID))
	}
	asyncDir := in.AsyncDir
	cancelled := func() bool { return in.Cancelled != nil && in.Cancelled() }
	var snapshot *AsyncStatus
	if in.FindPendingAsks != nil {
		snapshot, _ = readStatus(asyncDir)
	}
	if snapshot != nil && snapshot.Mode == "single" && (in.CurrentSessionID == "" || snapshot.SessionID == in.CurrentSessionID) &&
		(snapshot.State == "running" || snapshot.State == "queued") && len(snapshot.Steps) == 1 && (in.Index == nil || *in.Index == 0) &&
		stepRunnable(snapshot.Steps[0].Status) {
		if asks := in.FindPendingAsks(snapshot.RunID, snapshot.Steps[0].Agent, 0); len(asks) > 0 {
			what, which := "a pending supervisor ask", ""
			if len(asks) != 1 {
				what, which = "ambiguous pending supervisor asks", " to the intended request ID"
			}
			return steerFail(fmt.Sprintf("Steering not delivered or queued: async run %s is blocked on %s. No reply or recovery was attempted. Reply explicitly%s:\n%s",
				snapshot.RunID, what, which, supervisorReplyCommands(asks)))
		}
	}
	// The read-only snapshot precedes even stale-run reconciliation. An ask arriving later does not make a runner
	// acknowledgement proof of consumption or unblocking.
	rec, err := reconcileAsyncRun(asyncDir, ReconcileOptions{ResultsDir: in.ResultsDir, Kill: in.Kill})
	if err != nil {
		return steerFail(err.Error())
	}
	status := rec.Status
	if in.CurrentSessionID != "" && (status == nil || status.SessionID != in.CurrentSessionID) {
		return steerFail(fmt.Sprintf("Async run '%s' was not found in the active session.", in.RunID))
	}
	if status == nil {
		return steerFail(fmt.Sprintf("Async run '%s' was not found.", in.RunID))
	}
	steps := status.Steps
	if status.State != "running" && status.State != "queued" {
		if in.Mode != steerModeFollowUp || !canQueueRetainedAsyncFollowUp(status, in.Index) {
			return steerFail(fmt.Sprintf("Async run '%s' is not running or queued and cannot be steered.", in.RunID))
		}
		req := steerRequest{Type: "steer", ID: randomUUID(), TS: float64(nowMs()), Message: jsTrim(in.Message), Mode: steerModeFollowUp, TargetIndex: new(0), Source: "steer-action"}
		if _, err := queueRevivalBrief(asyncDir, req); err != nil {
			return steerFail(fmt.Sprintf("Failed to queue retained follow-up for async run %s: %s", status.RunID, err.Error()))
		}
		ledger := statusSteering(status)
		if ledger == nil {
			ledger = createSteeringStatus()
		}
		recordSteeringRequest(ledger, steerRequestInput{ID: req.ID, RequestedAt: int64(req.TS), Source: req.Source, Message: req.Message, Targets: []steeringTarget{{Index: 0, State: "scheduled"}}})
		setStatusSteering(status, ledger)
		if err := writeStatus(asyncDir, status); err != nil {
			return steerFail(fmt.Sprintf("Failed to queue retained follow-up for async run %s: %s", status.RunID, err.Error()))
		}
		queued := &steerActionResult{RequestID: req.ID, State: "scheduled", DeliveryStatus: "queued", SourceRunID: status.RunID, Targets: []steerActionTarget{{Index: 0, State: "scheduled"}}}
		return steerReply{Text: steeringReceipt(req.Message, fmt.Sprintf("Follow-up queued for the next resume of retained async run %s (request %s).", status.RunID, req.ID)), Steering: queued}
	}
	if in.Index != nil {
		if *in.Index < 0 || *in.Index >= len(steps) {
			return steerFail(fmt.Sprintf("Async run '%s' has %d children. Index %d is out of range.", status.RunID, len(steps), *in.Index))
		}
		if target := steps[*in.Index]; !stepRunnable(target.Status) {
			return steerFail(fmt.Sprintf("Async run '%s' child %d is %s and cannot be steered.", status.RunID, *in.Index, target.Status))
		}
	} else if len(runningIndexes(steps)) == 0 && len(steps) > 1 {
		return steerFail(fmt.Sprintf("Async run '%s' has no running child yet. Provide index to steer a queued child.", status.RunID))
	}
	running := runningIndexes(steps)
	effective := in.Index
	if effective == nil && status.Mode == "single" && len(running) == 0 && len(steps) > 0 && steps[0].Status == "pending" {
		effective = new(0)
	}
	targetIndexes := running
	if effective != nil {
		targetIndexes = []int{*effective}
	}
	if len(targetIndexes) == 0 {
		return steerFail(fmt.Sprintf("Async run '%s' has no running child to steer.", status.RunID))
	}
	requestID := randomUUID()
	payload := steerPayload{Message: in.Message, Mode: in.Mode, Source: "steer-action", ID: requestID}
	if effective != nil {
		payload.TargetIndex = effective
	} else {
		payload.TargetIndexes, payload.HasTargetIndexes = targetIndexes, true
	}
	requestPath, err := requestAsyncSteer(asyncDir, payload)
	if err != nil {
		return steerFail(fmt.Sprintf("Failed to queue steering for async run %s: %s", status.RunID, err.Error()))
	}
	if in.OnRequestQueued != nil {
		in.OnRequestQueued(requestPath)
	}
	if in.Touch != nil {
		in.Touch()
	}
	targets := make([]steerActionTarget, len(targetIndexes))
	for i, ix := range targetIndexes {
		state := "pending"
		if ix < len(status.Steps) && status.Steps[ix].Status == "pending" {
			state = "scheduled"
		}
		targets[i] = steerActionTarget{Index: ix, State: state}
	}
	if allTargets(targets, func(s string) bool { return s == "scheduled" }) {
		scheduled := &steerActionResult{RequestID: requestID, State: "scheduled", DeliveryStatus: "queued", SourceRunID: status.RunID, Targets: targets}
		return steerReply{Text: steeringReceipt(in.Message, fmt.Sprintf("Steering scheduled for async run %s (request %s).", status.RunID, requestID)), Steering: scheduled}
	}
	ack := int64(3000)
	if in.AckTimeoutMs != nil {
		ack = *in.AckTimeoutMs
	}
	result := waitForSteeringAction(asyncDir, status.RunID, requestID, ack, cancelled)
	if result == nil {
		result = &steerActionResult{RequestID: requestID, State: "pending", DeliveryStatus: "queued", SourceRunID: status.RunID, Targets: targets}
	}
	if cancelled() {
		return steerReply{Text: steeringReceipt(in.Message, fmt.Sprintf("Steering pending for async run %s (request %s); caller aborted before recovery.", status.RunID, requestID)), Steering: result}
	}
	finalStatus, _ := readStatus(asyncDir)
	var finalLedger *steeringStatus
	var finalResult *steerActionResult
	if finalStatus != nil {
		if finalLedger = statusSteering(finalStatus); finalLedger != nil {
			finalResult = actionResultFromSteeringStatus(finalLedger, status.RunID, requestID, "")
		}
	}
	acceptedReply := func(accepted *steerActionResult) steerReply {
		state := "queued"
		if accepted.State == "delivered" {
			state = "delivered"
		}
		return steerReply{Text: steeringReceipt(in.Message, fmt.Sprintf("Steering %s for async run %s (request %s).", state, status.RunID, requestID)), Steering: accepted}
	}
	if steerTargetsAccepted(finalResult) {
		return acceptedReply(finalResult)
	}
	runningSteps := status.Steps
	if finalStatus != nil {
		runningSteps = finalStatus.Steps
	}
	runningCount := len(runningIndexes(runningSteps))
	recoveryAllowed := (in.Mode == "" || in.Mode == steerModeSteer) && status.Mode == "single" && !statusExtraBool(status, "isNested") && runningCount == 1 &&
		finalLedger != nil && (in.Index == nil || *in.Index == 0)
	if recoveryAllowed && (finalResult == nil || finalResult.State != "scheduled") && in.Recover != nil {
		return steerRecover(in, asyncDir, status, requestID, cancelled, acceptedReply)
	}
	stateText := "pending"
	switch {
	case result.State == "failed":
		stateText = "failed"
	case result.State == "partial":
		stateText = "partial"
	case result.DeliveryStatus == "queued":
		stateText = "queued"
	case result.State == "delivered":
		stateText = "delivered"
	case result.State == "scheduled":
		stateText = "scheduled"
	case result.State == "recovered":
		stateText = "recovered"
	}
	return steerReply{Text: steeringReceipt(in.Message, fmt.Sprintf("Steering %s for async run %s (request %s).", stateText, status.RunID, requestID)),
		IsError: result.State == "failed" || result.State == "partial", Steering: result}
}

// steerRecover pauses a single-child run whose runner did not acknowledge the steer and revives it with the steer as the follow-up.
// The recovery claim is the one that survives an unconfirmed pause: a delayed pause must not allow a second replacement.
func steerRecover(in steerRunInput, asyncDir string, status *AsyncStatus, requestID string, cancelled func() bool, acceptedReply func(*steerActionResult) steerReply) steerReply {
	appendNotice := func(state, message string) {
		n := steeringNotice{Type: "subagent.steering.notice", TS: nowMs(), RunID: status.RunID, RequestID: requestID, State: state, Message: message, CurrentSessionID: status.SessionID}
		steeringNoticeLine(asyncDir, n)
	}
	reply, err := func() (steerReply, error) {
		latest, _ := readStatus(asyncDir)
		if latest != nil {
			if l := statusSteering(latest); l != nil {
				if r := actionResultFromSteeringStatus(l, status.RunID, requestID, ""); steerTargetsAccepted(r) {
					return acceptedReply(r), nil
				}
			}
		}
		committedAt := nowMs()
		if in.OnBeforeRecoveryClaim != nil {
			in.OnBeforeRecoveryClaim(requestID, committedAt)
		}
		claimPath, markerPath, err := claimSteeringRecovery(asyncDir, steeringClaim{RequestID: requestID, SourceRunID: status.RunID, CommittedAt: committedAt})
		if err != nil {
			return steerReply{}, err
		}
		if in.OnRecoveryCommitted != nil {
			in.OnRecoveryCommitted(requestID, committedAt)
		}
		if pre, _ := readStatus(asyncDir); pre != nil {
			if l := statusSteering(pre); l != nil {
				if r := actionResultFromSteeringStatus(l, status.RunID, requestID, ""); r != nil && len(r.Targets) > 0 && everyDeliveredBy(r, committedAt) {
					_ = os.Remove(markerPath)
					_ = os.Remove(claimPath)
					return acceptedReply(r), nil
				}
			}
		}
		if _, err := requestAsyncInterrupt(asyncDir, "steering-recovery"); err != nil {
			_ = os.Remove(markerPath)
			_ = os.Remove(claimPath)
			return steerReply{}, fmt.Errorf("Failed to commit steering recovery interrupt: %s", err.Error())
		}
		recoveryTimeout := int64(15000)
		if in.RecoveryTimeoutMs != nil {
			recoveryTimeout = *in.RecoveryTimeoutMs
		}
		pauseDeadline := time.Now().Add(time.Duration(recoveryTimeout) * time.Millisecond)
		var paused *AsyncStatus
		for !time.Now().After(pauseDeadline) {
			if cancelled() {
				break
			}
			if c, _ := readStatus(asyncDir); c != nil && c.State == "paused" && c.EndedAt != nil {
				paused = c
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if paused == nil {
			return steerReply{}, errors.New("Source run did not reach confirmed paused state within 15 seconds; no replacement was launched and the recovery claim remains committed to prevent a delayed duplicate.")
		}
		target, err := resolveAsyncResumeTarget(resumeParams{ID: status.RunID}, in.Dirs, resumeOptions{SessionID: in.CurrentSessionID})
		if err != nil {
			return steerReply{}, fmt.Errorf("Source run remains paused and cannot be revived safely: %s", err.Error())
		}
		if target.kind != "revive" || target.sessionFile == "" {
			return steerReply{}, errors.New("Source run has no valid persisted child session; it remains paused and requires a new run.")
		}
		if target.descriptor == nil {
			return steerReply{}, errors.New("Source run has no private recovery descriptor; it remains paused and is not safely resumable.")
		}
		_, initial := recoveryInitialToolBudget(target.descriptor)
		var deadline *int64
		if target.descriptor.AbsoluteDeadlineAt != nil {
			deadline = new(int64(*target.descriptor.AbsoluteDeadlineAt))
		}
		limits, err := remainingSteeringRecoveryLimits(deadline, initial, statusConsumedToolCount(paused), nowMs())
		if err != nil {
			return steerReply{}, err
		}
		revived := in.Recover(limits)
		if revived.AsyncID == "" {
			if revived.Error != "" {
				return steerReply{}, errors.New(revived.Error)
			}
			return steerReply{}, errors.New("Replacement launch failed; source run remains paused.")
		}
		targetIndex := -1
		if in.Index != nil {
			targetIndex = *in.Index
		} else if l := statusSteering(paused); l != nil {
			if req := findSteeringRequest(l, requestID); req != nil && len(req.Targets) > 0 {
				targetIndex = req.Targets[0].Index
			}
		}
		if targetIndex < 0 && in.Index == nil {
			for i, s := range status.Steps {
				if s.Status == "running" {
					targetIndex = i
					break
				}
			}
		}
		if l := statusSteering(paused); l != nil && targetIndex >= 0 {
			updateSteeringTarget(l, requestID, targetIndex, "recovered", nowMs(), steeringTargetFields{ReplacementRunID: revived.AsyncID})
			setStatusSteering(paused, l)
			if targetIndex < len(paused.Steps) {
				if ss := stepSteering(&paused.Steps[targetIndex]); ss != nil {
					updateSteeringTarget(ss, requestID, targetIndex, "recovered", nowMs(), steeringTargetFields{ReplacementRunID: revived.AsyncID})
					setStepSteering(&paused.Steps[targetIndex], ss)
				}
			}
			if err := writeStatus(asyncDir, paused); err != nil {
				return steerReply{}, err
			}
		}
		var recovered *steerActionResult
		if l := statusSteering(paused); l != nil {
			recovered = actionResultFromSteeringStatus(l, status.RunID, requestID, revived.AsyncID)
		}
		appendNotice("recovered", fmt.Sprintf("Steering recovered for run %s; replacement %s launched.", status.RunID, revived.AsyncID))
		if recovered == nil {
			first := 0
			if in.Index != nil {
				first = *in.Index
			}
			recovered = &steerActionResult{RequestID: requestID, State: "recovered", SourceRunID: status.RunID, ReplacementRunID: revived.AsyncID, DeliveryStatus: "delivered", Targets: []steerActionTarget{{Index: first, State: "recovered"}}}
		}
		return steerReply{Text: steeringReceipt(in.Message, fmt.Sprintf("Steering recovered for async run %s; replacement %s launched after the source paused.", status.RunID, revived.AsyncID)), Steering: recovered}, nil
	}()
	if err == nil {
		return reply
	}
	reason := err.Error()
	failedStatus, _ := readStatus(asyncDir)
	targetIndex := -1
	if in.Index != nil {
		targetIndex = *in.Index
	} else {
		for i, s := range status.Steps {
			if s.Status == "running" {
				targetIndex = i
				break
			}
		}
	}
	if failedStatus != nil && failedStatus.State != "running" && failedStatus.State != "queued" && targetIndex >= 0 {
		if l := statusSteering(failedStatus); l != nil {
			updateSteeringTarget(l, requestID, targetIndex, "failed", nowMs(), steeringTargetFields{Reason: reason})
			setStatusSteering(failedStatus, l)
			if targetIndex < len(failedStatus.Steps) {
				if ss := stepSteering(&failedStatus.Steps[targetIndex]); ss != nil {
					updateSteeringTarget(ss, requestID, targetIndex, "failed", nowMs(), steeringTargetFields{Reason: reason})
					setStepSteering(&failedStatus.Steps[targetIndex], ss)
				}
			}
			failedStatus.ActivityState = "needs_attention"
			_ = writeStatus(asyncDir, failedStatus)
		}
	}
	var failed *steerActionResult
	if failedStatus != nil {
		if l := statusSteering(failedStatus); l != nil {
			failed = actionResultFromSteeringStatus(l, status.RunID, requestID, "")
		}
	}
	appendNotice("failed", fmt.Sprintf("Steering failed for run %s: %s", status.RunID, reason))
	if failed == nil {
		first := 0
		if in.Index != nil {
			first = *in.Index
		}
		failed = &steerActionResult{RequestID: requestID, State: "failed", SourceRunID: status.RunID, DeliveryStatus: "queued", Targets: []steerActionTarget{{Index: first, State: "failed", Reason: reason}}}
	}
	return steerReply{Text: steeringReceipt(in.Message, fmt.Sprintf("Steering failed for async run %s (request %s): %s", status.RunID, requestID, reason)), IsError: true, Steering: failed}
}

// everyDeliveredBy is whether every target was delivered no later than at.
func everyDeliveredBy(r *steerActionResult, at int64) bool {
	for _, t := range r.Targets {
		if t.State != "queued" && (t.DeliveredAt == nil || *t.DeliveredAt > at) {
			return false
		}
	}
	return true
}
