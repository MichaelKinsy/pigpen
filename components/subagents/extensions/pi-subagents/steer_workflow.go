package pi_subagents

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Port of src/runs/foreground/workflow-foreground-steering.ts, steerWorkflowChildByKey of subagent-executor.ts and
// src/runs/shared/workflow-async-child-guidance.ts: steering a workflow (a scripted run) and its children.

// workflowSteerTarget is a live foreground child of a workflow with the run it is addressed as.
type workflowSteerTarget struct {
	Control     *ForegroundControl
	WorkflowRun string
	SourceRunID string
}

func foregroundControlLiveInWorkflow(c *ForegroundControl, workflowRunID, sessionID string) bool {
	return c.ParentWorkflowRunID == workflowRunID && c.SessionID == sessionID && c.LiveChildCount() > 0
}

// activeWorkflowError is why a workflow cannot be steered through a live foreground child ("" when it can).
func activeWorkflowError(state *SubagentState, workflowRunID, asyncDirRoot string) string {
	if state.CurrentSessionID == "" {
		return "Workflow steering requires an active parent session."
	}
	if _, ok := state.WorkflowControllers[workflowRunID]; !ok {
		return fmt.Sprintf("Workflow '%s' has no live foreground child.", workflowRunID)
	}
	st, _ := readStatus(filepath.Join(asyncDirRoot, workflowRunID))
	if st == nil || st.Mode != "workflow" || (st.State != "running" && st.State != "queued") {
		return fmt.Sprintf("Workflow '%s' has no live foreground child.", workflowRunID)
	}
	if st.SessionID != state.CurrentSessionID {
		return fmt.Sprintf("Workflow '%s' was not found in the active session.", workflowRunID)
	}
	return ""
}

// resolveWorkflowForegroundSteeringTarget finds the live foreground child a steer of a child run id or of a workflow run id means:
// exactly one, never a guess among several.
func resolveWorkflowForegroundSteeringTarget(state *SubagentState, childRunID, workflowRunID, asyncDirRoot string) (*workflowSteerTarget, string) {
	if childRunID != "" {
		c := state.ForegroundControls[childRunID]
		if c == nil || c.ParentWorkflowRunID == "" {
			return nil, fmt.Sprintf("Foreground run '%s' is not a live workflow-owned child.", childRunID)
		}
		wf := c.ParentWorkflowRunID
		if msg := activeWorkflowError(state, wf, asyncDirRoot); msg != "" {
			return nil, msg
		}
		if !foregroundControlLiveInWorkflow(c, wf, state.CurrentSessionID) {
			return nil, fmt.Sprintf("Foreground run '%s' is not a live workflow-owned child in the active session.", childRunID)
		}
		return &workflowSteerTarget{c, wf, childRunID}, ""
	}
	if workflowRunID == "" {
		return nil, "Workflow steering requires a workflow or child run id."
	}
	if msg := activeWorkflowError(state, workflowRunID, asyncDirRoot); msg != "" {
		return nil, msg
	}
	var live []*ForegroundControl
	for _, id := range sortedKeys(state.ForegroundControls) {
		if c := state.ForegroundControls[id]; foregroundControlLiveInWorkflow(c, workflowRunID, state.CurrentSessionID) {
			live = append(live, c)
		}
	}
	switch {
	case len(live) == 0:
		return nil, fmt.Sprintf("Workflow '%s' has no live foreground child.", workflowRunID)
	case len(live) > 1:
		return nil, fmt.Sprintf("Workflow '%s' has %d live foreground children; steer a child run id instead.", workflowRunID, len(live))
	}
	return &workflowSteerTarget{live[0], workflowRunID, workflowRunID}, ""
}

var asyncChildRunIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// workflowAsyncChildSteeringGuidance lists the async children a running workflow's status records as running, as exact child ids to
// steer. It is projection only: not a control route and not proof that a child is alive.
func workflowAsyncChildSteeringGuidance(st *AsyncStatus, state *SubagentState) []string {
	if st.Mode != "workflow" || (st.State != "running" && st.State != "queued") || state == nil || state.CurrentSessionID == "" || st.SessionID != state.CurrentSessionID {
		return nil
	}
	if _, ok := state.WorkflowControllers[st.RunID]; !ok {
		return nil
	}
	var ids []string
	seen := map[string]bool{}
	for _, step := range st.Steps {
		if !statusStepAsync(&step) || step.Status != "running" || stepRunnerType(&step) == "external-cli" || stepRunnerType(&step) == "external-job" {
			continue
		}
		id := step.RunID
		if id == "" || !asyncChildRunIDPattern.MatchString(id) || id == st.RunID || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	lines := []string{"Async children recorded as running; direct steering rechecks controls. Choose an exact child ID (queued does not mean consumed):"}
	for _, id := range ids {
		lines = append(lines, fmt.Sprintf(`  subagent({ action: "steer", id: "%s", mode: "follow_up", message: "..." })`, id))
	}
	return lines
}

func statusStepAsync(s *AsyncStep) bool { return s.Async != nil && *s.Async }

// stepRunnerType is the runner.type a step's status records ("" for a Pi child).
func stepRunnerType(s *AsyncStep) string {
	var r struct {
		Type string `json:"type"`
	}
	if raw, ok := s.Extra["runner"]; ok {
		_ = json.Unmarshal(raw, &r)
	}
	return r.Type
}

// steerWorkflowRunInput is the input of steerWorkflowRun.
type steerWorkflowRunInput struct {
	State     *SubagentState
	RunID     string
	AsyncDir  string
	Message   string
	Mode      steerDeliveryMode
	Index     *float64
	Cancelled func() bool
	// AckTimeoutMs defaults to 3 s.
	AckTimeoutMs *int64
}

// steerWorkflowRun steers a workflow run. A local controller stays authoritative; a workflow recorded as owned by another process
// accepts a request into its inbox (enqueue, never takeover and never a proof of liveness).
func steerWorkflowRun(in steerWorkflowRunInput) steerReply {
	state := in.State
	st, err := readStatus(in.AsyncDir)
	if err != nil {
		return steerFail(err.Error())
	}
	if st == nil || st.Mode != "workflow" || st.RunID != in.RunID || filepath.Base(in.AsyncDir) != in.RunID {
		return steerFail(fmt.Sprintf("Workflow '%s' does not match its resolved run directory.", in.RunID))
	}
	if _, local := state.WorkflowControllers[in.RunID]; local {
		target, msg := resolveWorkflowForegroundSteeringTarget(state, "", in.RunID, filepath.Dir(in.AsyncDir))
		if target == nil {
			return steerFail(strings.Join(append([]string{msg}, workflowAsyncChildSteeringGuidance(st, state)...), "\n"))
		}
		var idx *int
		if in.Index != nil {
			if i, ok := floatIndex(*in.Index); ok {
				idx = &i
			} else {
				return steerFail(fmt.Sprintf("Foreground run '%s' child %s is not live.", target.Control.RunID, jsNumber(*in.Index)))
			}
		}
		return steerWorkflowForegroundTarget(target, in.Message, in.Mode, idx)
	}
	if state.CurrentSessionID == "" {
		return steerFail("Workflow steering requires an active parent session.")
	}
	if st.SessionID != state.CurrentSessionID {
		return steerFail(fmt.Sprintf("Workflow '%s' was not found in the active session.", in.RunID))
	}
	if st.State != "running" && st.State != "queued" {
		return steerFail(fmt.Sprintf("Workflow '%s' is not running or queued and cannot be steered.", in.RunID))
	}
	owner := st.CompletionOwnerID
	localOwner := state.CompletionOwnerID
	if localOwner == "" {
		localOwner = completionOwnerID()
	}
	liveLocal := false
	for _, c := range state.ForegroundControls {
		liveLocal = liveLocal || c.ParentWorkflowRunID == in.RunID
	}
	if jsTrim(owner) == "" || owner == localOwner || liveLocal {
		return steerFail(fmt.Sprintf("Workflow '%s' has no live foreground child and is not a recorded foreign workflow.", in.RunID))
	}
	var targetIndex *int
	if in.Index != nil {
		i, ok := floatIndex(*in.Index)
		if !ok || i >= len(st.Steps) {
			return steerFail(fmt.Sprintf("Workflow '%s' index %s is out of range or invalid.", in.RunID, jsNumber(*in.Index)))
		}
		step := st.Steps[i]
		if !stepRunnable(step.Status) {
			return steerFail(fmt.Sprintf("Workflow '%s' child %d is %s and cannot be steered.", in.RunID, i, step.Status))
		}
		if jsTrim(step.WorkflowKey) == "" {
			return steerFail(fmt.Sprintf("Workflow '%s' child %d has no projected workflow key.", in.RunID, i))
		}
		targetIndex = &i
	}
	requestID := randomUUID()
	if _, err := requestAsyncSteer(in.AsyncDir, steerPayload{ID: requestID, Message: in.Message, Mode: in.Mode, TargetIndex: targetIndex, Source: "steer-action"}); err != nil {
		return steerFail(fmt.Sprintf("Failed to queue steering for workflow %s: %s", in.RunID, err.Error()))
	}
	// Only the owner writes the ledger. No acknowledgment leaves an honest, unaddressed queued receipt.
	ack := int64(3000)
	if in.AckTimeoutMs != nil {
		ack = *in.AckTimeoutMs
	}
	steering := waitForSteeringAction(in.AsyncDir, in.RunID, requestID, ack, in.Cancelled)
	if steering == nil {
		steering = &steerActionResult{RequestID: requestID, State: "pending", DeliveryStatus: "queued", SourceRunID: in.RunID, Targets: []steerActionTarget{}}
		if targetIndex != nil {
			steering.Targets = []steerActionTarget{{Index: *targetIndex, State: "pending"}}
		}
	}
	failed := steering.State == "failed" || steering.State == "partial"
	word := "queued"
	switch {
	case failed:
		word = steering.State
	case steering.DeliveryStatus == "delivered":
		word = "delivered"
	}
	return steerReply{Text: steeringReceipt(in.Message, fmt.Sprintf("Steering %s for workflow %s (request %s).", word, in.RunID, requestID)), IsError: failed, Steering: steering}
}

func floatIndex(f float64) (int, bool) {
	if f != float64(int(f)) || f < 0 || f > 1<<31 {
		return 0, false
	}
	return int(f), true
}

// steerWorkflowForegroundTarget steers a live workflow-owned foreground child through its session: `steer` (and `auto`) interrupt
// the child at its next safe point, `follow_up` queues the message until the current run settles.
func steerWorkflowForegroundTarget(target *workflowSteerTarget, message string, mode steerDeliveryMode, index *int) steerReply {
	c := target.Control
	var active []int
	for i := range c.ActiveChildren {
		active = append(active, i)
	}
	sort.Ints(active)
	var ix int
	switch {
	case index != nil:
		ix = *index
	case len(active) == 1:
		ix = active[0]
	case len(active) == 0:
		return steerFail(fmt.Sprintf("Foreground run '%s' has no live child session.", c.RunID))
	default:
		return steerFail(fmt.Sprintf("Foreground run '%s' has %d live child sessions; provide index.", c.RunID, len(active)))
	}
	child := c.ActiveChildren[ix]
	if child == nil {
		return steerFail(fmt.Sprintf("Foreground run '%s' child %d is not live.", c.RunID, ix))
	}
	if child.Steer == nil {
		return steerFail(fmt.Sprintf("Foreground run '%s' child %d does not support steering.", c.RunID, ix))
	}
	message = jsTrim(message)
	requestID := randomUUID()
	passMode := ""
	if mode != "" && mode != steerModeSteer {
		passMode = string(mode)
	}
	state, reason := child.Steer(message, passMode)
	var tgt steerActionTarget
	switch state {
	case "delivered":
		tgt = steerActionTarget{Index: ix, State: "delivered", DeliveredAt: new(time.Now().UnixMilli())}
	case "queued":
		tgt = steerActionTarget{Index: ix, State: "queued"}
	default:
		tgt = steerActionTarget{Index: ix, State: "failed", Reason: reason}
	}
	steering := &steerActionResult{RequestID: requestID, State: "pending", DeliveryStatus: "queued", SourceRunID: target.SourceRunID, Targets: []steerActionTarget{tgt}}
	switch state {
	case "delivered":
		steering.State, steering.DeliveryStatus = "delivered", "delivered"
		return steerReply{Text: steeringReceipt(message, fmt.Sprintf("Steering delivered for foreground run %s (request %s).", c.RunID, requestID)), Steering: steering}
	case "queued":
		return steerReply{Text: steeringReceipt(message, fmt.Sprintf("Steering queued for foreground run %s (request %s).", c.RunID, requestID)), Steering: steering}
	}
	steering.State = "failed"
	if reason == "" {
		reason = "unknown error"
	}
	return steerReply{Text: steeringReceipt(message, fmt.Sprintf("Steering failed for foreground run %s (request %s): %s", c.RunID, requestID, reason)), IsError: true, Steering: steering}
}

// ---- steering a workflow child by its stable key (runs.steer of a workflow script) ----

const childSessionNotRunningYet = "Child session is not running yet."

// steerKeyOptions is WorkflowSteerOptions.
type steerKeyOptions struct {
	Mode         steerDeliveryMode
	Index        *int
	AckTimeoutMs *int64
}

// steerKeyResult is WorkflowSteerResult: "delivered", "queued", "failed" or "missed" (no live route).
type steerKeyResult struct {
	Key            string
	State          string
	RequestID      string
	DeliveryStatus string
	Targets        []steerActionTarget
	Error          string
}

func workflowSteerReceipt(key string, r steerReply) steerKeyResult {
	if r.Steering == nil {
		return steerKeyResult{Key: key, State: "failed", Error: r.Text}
	}
	out := steerKeyResult{Key: key, RequestID: r.Steering.RequestID, DeliveryStatus: r.Steering.DeliveryStatus}
	switch {
	case r.IsError || r.Steering.State == "failed" || r.Steering.State == "partial":
		out.State = "failed"
		out.Error = r.Text
	case r.Steering.DeliveryStatus == "delivered":
		out.State = "delivered"
	default:
		out.State = "queued"
	}
	for _, t := range r.Steering.Targets {
		out.Targets = append(out.Targets, steerActionTarget{Index: t.Index, State: t.State, Reason: t.Reason})
	}
	return out
}

type steerByKeyInput struct {
	State         *SubagentState
	WorkflowRunID string
	Key           string
	Message       string
	Options       steerKeyOptions
	Cancelled     func() bool
	AsyncDirRoot  string
	ResolveRunID  func() string
	// ExternalControlError is the refusal of a steer of an external runner ("" when the run takes one).
	ExternalControlError func(asyncDir string) string
}

// steerWorkflowChildByKey routes a steer to the live foreground child registered under a workflow key, else to the async child
// recorded for it; a child that already ended is reported as missed, never queued behind a terminal run.
func steerWorkflowChildByKey(in steerByKeyInput) steerKeyResult {
	root := in.AsyncDirRoot
	if root == "" {
		root = defaultAsyncDirs().Async
	}
	ack := int64(3000)
	if in.Options.AckTimeoutMs != nil {
		ack = *in.Options.AckTimeoutMs
	}
	deadline := time.Now().Add(time.Duration(ack) * time.Millisecond)
	cancelled := func() bool { return in.Cancelled != nil && in.Cancelled() }
	for {
		var control *ForegroundControl
		for _, id := range sortedKeys(in.State.ForegroundControls) {
			c := in.State.ForegroundControls[id]
			if c.ParentWorkflowRunID == in.WorkflowRunID && c.WorkflowKey == in.Key && c.LiveChildCount() > 0 {
				control = c
				break
			}
		}
		if control != nil {
			r := steerWorkflowForegroundTarget(&workflowSteerTarget{control, in.WorkflowRunID, control.RunID}, in.Message, in.Options.Mode, in.Options.Index)
			retry := false
			if r.Steering != nil {
				for _, t := range r.Steering.Targets {
					retry = retry || t.Reason == childSessionNotRunningYet
				}
			}
			// The control registers before its child session exists; keep polling until the steer can route.
			if !retry || !time.Now().Before(deadline) {
				return workflowSteerReceipt(in.Key, r)
			}
		}
		wst, _ := readStatus(filepath.Join(root, in.WorkflowRunID))
		var step *AsyncStep
		if wst != nil {
			for i := range wst.Steps {
				if wst.Steps[i].WorkflowKey == in.Key {
					step = &wst.Steps[i]
					break
				}
			}
		}
		childRunID := ""
		if step != nil {
			childRunID = step.RunID
		}
		if childRunID == "" && in.ResolveRunID != nil {
			childRunID = in.ResolveRunID()
		}
		if childRunID != "" {
			dir := filepath.Join(root, childRunID)
			rec, _ := reconcileAsyncRun(dir, ReconcileOptions{Kill: asyncKill})
			cst := rec.Status
			if cst != nil && cst.State != "running" && cst.State != "queued" && (in.Options.Mode != steerModeFollowUp || !canQueueRetainedAsyncFollowUp(cst, in.Options.Index)) {
				return steerKeyResult{Key: in.Key, State: "missed", Error: fmt.Sprintf("Workflow child '%s' is %s.", in.Key, cst.State)}
			}
			if cst != nil {
				if in.ExternalControlError != nil {
					if msg := in.ExternalControlError(dir); msg != "" {
						return workflowSteerReceipt(in.Key, steerFail(msg))
					}
				}
				left := max(1, time.Until(deadline).Milliseconds())
				r := steerAsyncRun(steerRunInput{CurrentSessionID: in.State.CurrentSessionID, RunID: childRunID, Message: in.Message, Mode: in.Options.Mode, Index: in.Options.Index,
					AsyncDir: dir, Dirs: asyncDirs{Async: root}, Kill: asyncKill, Cancelled: in.Cancelled, AckTimeoutMs: &left})
				return workflowSteerReceipt(in.Key, r)
			}
		}
		if step != nil && !stepRunnable(step.Status) {
			return steerKeyResult{Key: in.Key, State: "missed", Error: fmt.Sprintf("Workflow child '%s' is %s.", in.Key, step.Status)}
		}
		if wst != nil && wst.State != "running" && wst.State != "queued" {
			return steerKeyResult{Key: in.Key, State: "missed", Error: fmt.Sprintf("Workflow '%s' is %s.", in.WorkflowRunID, wst.State)}
		}
		if cancelled() || !time.Now().Before(deadline) {
			return steerKeyResult{Key: in.Key, State: "missed", Error: fmt.Sprintf("Workflow child '%s' had no live steering route.", in.Key)}
		}
		time.Sleep(min(10*time.Millisecond, max(time.Millisecond, time.Until(deadline))))
	}
}
