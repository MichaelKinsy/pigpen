package pi_subagents

import (
	"fmt"
	"os"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Control of nested runs from the root (subagent-executor.ts: interruptNestedRun, resumeLiveNestedRun, steerNestedRun and their
// direct async fallbacks). A nested run is controlled through the route it reports to: a request file in the route's inbox that the
// run's owner answers with a result file; an owner that does not answer is not reachable, and only a nested async run with a live
// status of its own can be interrupted or steered directly.

// nestedTarget is a nested run found by id with the route it reports to.
type nestedTarget struct {
	Match nestedRunMatch
}

// nestedRoutesOf are the nested routes of the session's live foreground runs (the original's scope for the root executor).
func nestedRoutesOf(state *SubagentState) []*nestedRoute {
	var routes []*nestedRoute
	seen := map[string]bool{}
	for _, id := range sortedKeys(state.ForegroundControls) {
		if r := state.ForegroundControls[id].NestedRoute; r != nil && !seen[r.CapabilityToken] {
			seen[r.CapabilityToken] = true
			routes = append(routes, r)
		}
	}
	return routes
}

// resolveNestedTarget finds the one nested run an id means among the routes of the session; ambiguity is an error.
func resolveNestedTarget(state *SubagentState, id string) (*nestedTarget, error) {
	routes := nestedRoutesOf(state)
	if len(routes) == 0 || !isSafeNestedID(id) {
		return nil, nil
	}
	matches, err := findNestedRunMatchesByID(id, false, &nestedRunResolutionScope{Routes: routes})
	if err != nil || len(matches) == 0 {
		return nil, nil
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("Nested run id '%s' is ambiguous across nested routes. Use the full id from the status output.", id)
	}
	return &nestedTarget{matches[0]}, nil
}

// nestedChildOf is the id of the nested child `childID` of the root run runID ("" when it has none).
func nestedChildOf(rt *asyncRuntime, runID, childID string) string {
	route, err := findNestedRouteForRootID(runID)
	if err != nil || route == nil {
		return ""
	}
	reg, err := projectNestedEvents(route)
	if err != nil {
		return ""
	}
	if n := findNestedRun(reg.Children, childID); n != nil {
		return n.ID
	}
	return ""
}

const nestedControlWait = time.Second

// sendNestedControlRequest drops a control request and waits (1 s) for its answer; nil when the owner did not answer.
func sendNestedControlRequest(t *nestedTarget, action, message string, wait time.Duration) *nestedControlResult {
	route := t.Match.Route
	requestID := randomUUID()
	ignored, _ := snapshotNestedEventFiles(route)
	if _, err := writeNestedControlRequest(route, float64(nowMs()), requestID, t.Match.Run.ID, action, message); err != nil {
		return &nestedControlResult{OK: false, Message: err.Error()}
	}
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if r, _ := findNestedControlResult(route, requestID, t.Match.Run.ID, ignored); r != nil {
			return r
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil
}

func nestedFail(text string) steerReply { return steerFail(text) }

// directNestedAsyncInterrupt interrupts a nested async run whose own status says it is running, when its owner is not reachable.
func directNestedAsyncInterrupt(t *nestedTarget, kill killFunc) (text string, isError, handled bool) {
	run := t.Match.Run
	asyncDir := resolveNestedAsyncDir(t.Match.RootRunID, run)
	if asyncDir == "" {
		return "", false, false
	}
	_, results := nestedRunScope(t.Match.RootRunID)
	rec, _ := reconcileAsyncRun(asyncDir, ReconcileOptions{ResultsDir: results, Kill: kill})
	st := rec.Status
	pid := 0
	if st != nil && st.PID != nil && *st.PID > 0 {
		pid = *st.PID
	} else if run.PID != nil {
		pid = int(*run.PID)
	}
	if st == nil || st.State != "running" || pid <= 0 {
		return "", false, false
	}
	if _, err := requestAsyncInterrupt(asyncDir, "nested-interrupt"); err != nil {
		return fmt.Sprintf("Failed to interrupt nested async run %s: %s", run.ID, err.Error()), true, true
	}
	return fmt.Sprintf("Interrupt requested for nested async run %s.", run.ID), false, true
}

// interruptNestedRun is the interrupt action on a nested run.
func interruptNestedRun(t *nestedTarget, kill killFunc, wait time.Duration) (string, bool) {
	run := t.Match.Run
	switch run.State {
	case "complete":
		return fmt.Sprintf("Nested run %s is already complete and cannot be interrupted.", run.ID), true
	case "failed":
		return fmt.Sprintf("Nested run %s has failed and cannot be interrupted.", run.ID), true
	case "paused":
		return fmt.Sprintf("Nested run %s is already paused.", run.ID), true
	}
	if r := sendNestedControlRequest(t, "interrupt", "", wait); r != nil {
		return r.Message, !r.OK
	}
	if text, isErr, handled := directNestedAsyncInterrupt(t, kill); handled {
		return text, isErr
	}
	return fmt.Sprintf("Nested run %s owner is not reachable and no safe direct async interrupt fallback is available.", run.ID), true
}

// resumeLiveNestedRun routes a resume of a nested run that is still live to its owner.
func resumeLiveNestedRun(t *nestedTarget, message string, wait time.Duration) (string, bool) {
	if r := sendNestedControlRequest(t, "resume", message, wait); r != nil {
		return r.Message, !r.OK
	}
	return fmt.Sprintf("Nested run %s appears live but its owner route is not reachable. Wait for completion, then retry action='resume'.", t.Match.Run.ID), true
}

// directNestedAsyncSteer steers a nested async run through the steer inbox of its own run directory (handled false: it has none).
func directNestedAsyncSteer(t *nestedTarget, message string, mode steerDeliveryMode, index *int, cancelled func() bool, kill killFunc) (steerReply, bool) {
	run := t.Match.Run
	asyncDir := resolveNestedAsyncDir(t.Match.RootRunID, run)
	if asyncDir == "" {
		return steerReply{}, false
	}
	_, results := nestedRunScope(t.Match.RootRunID)
	rec, _ := reconcileAsyncRun(asyncDir, ReconcileOptions{ResultsDir: results, Kill: kill})
	st := rec.Status
	if st == nil || (st.State != "running" && st.State != "queued") {
		return steerReply{}, false
	}
	steps := st.Steps
	if index != nil {
		if *index < 0 || *index >= len(steps) {
			return nestedFail(fmt.Sprintf("Nested async run %s has %d children. Index %d is out of range.", run.ID, len(steps), *index)), true
		}
		if s := steps[*index]; !stepRunnable(s.Status) {
			return nestedFail(fmt.Sprintf("Nested async run %s child %d is %s and cannot be steered.", run.ID, *index, s.Status)), true
		}
	}
	running := runningIndexes(steps)
	effective := index
	if effective == nil && st.Mode == "single" && len(running) == 0 && len(steps) > 0 && steps[0].Status == "pending" {
		effective = new(0)
	}
	targets := running
	if effective != nil {
		targets = []int{*effective}
	}
	if len(targets) == 0 {
		return nestedFail(fmt.Sprintf("Nested async run %s has no running child to steer.", run.ID)), true
	}
	requestID := randomUUID()
	payload := steerPayload{Message: message, Mode: mode, Source: "nested-steer", ID: requestID}
	if effective != nil {
		payload.TargetIndex = effective
	} else {
		payload.TargetIndexes, payload.HasTargetIndexes = targets, true
	}
	if _, err := requestAsyncSteer(asyncDir, payload); err != nil {
		return nestedFail(fmt.Sprintf("Failed to queue steering for nested async run %s: %s", run.ID, err.Error())), true
	}
	plan := make([]steerActionTarget, len(targets))
	for i, ix := range targets {
		state := "pending"
		if ix < len(steps) && steps[ix].Status == "pending" {
			state = "scheduled"
		}
		plan[i] = steerActionTarget{Index: ix, State: state}
	}
	if allTargets(plan, func(s string) bool { return s == "scheduled" }) {
		scheduled := &steerActionResult{RequestID: requestID, State: "scheduled", DeliveryStatus: "queued", SourceRunID: run.ID, Targets: plan}
		return steerReply{Text: steeringReceipt(message, fmt.Sprintf("Steering scheduled for nested async run %s (request %s).", run.ID, requestID)), Steering: scheduled}, true
	}
	result := waitForSteeringAction(asyncDir, run.ID, requestID, 3000, cancelled)
	if result == nil {
		result = &steerActionResult{RequestID: requestID, State: "pending", DeliveryStatus: "queued", SourceRunID: run.ID, Targets: plan}
	}
	word := "pending"
	switch {
	case result.State == "failed":
		word = "failed"
	case result.State == "partial":
		word = "partial"
	case result.DeliveryStatus == "queued":
		word = "queued"
	case result.State == "delivered":
		word = "delivered"
	}
	return steerReply{Text: steeringReceipt(message, fmt.Sprintf("Steering %s for nested async run %s (request %s).", word, run.ID, requestID)),
		IsError: result.State == "failed" || result.State == "partial", Steering: result}, true
}

// steerNestedRun is the steer action on a nested run.
func steerNestedRun(t *nestedTarget, message string, mode steerDeliveryMode, index *int, cancelled func() bool, kill killFunc) steerReply {
	run := t.Match.Run
	if run.State != "running" && run.State != "queued" {
		return nestedFail(fmt.Sprintf("Nested run %s is %s and cannot be steered.", run.ID, run.State))
	}
	if r, handled := directNestedAsyncSteer(t, message, mode, index, cancelled, kill); handled {
		return r
	}
	return nestedFail(fmt.Sprintf("Nested run %s is not a live async Pi child session with a steering inbox. action='steer' cannot target foreground nested runs.", run.ID))
}

// steerNestedAction is the steer action when the id names a nested run of this session (handled false: it does not).
func steerNestedAction(rt *asyncRuntime, id, message string, mode steerDeliveryMode, index *int, cancelled func() bool) (steerReply, bool) {
	rt.mu.Lock()
	target, err := resolveNestedTarget(rt.state, id)
	rt.mu.Unlock()
	if err != nil {
		return steerFail(err.Error()), true
	}
	if target == nil {
		return steerReply{}, false
	}
	return steerNestedRun(target, message, mode, index, cancelled, asyncKill), true
}

func nestedToolResult(text string, isError bool) (any, error) {
	if isError {
		return nil, mgmtError(text)
	}
	return explicitOK{Content: text, Details: map[string]any{"mode": "management", "results": []any{}}}, nil
}

// nestedAwareAction wraps the interrupt (or resume) action of the background runs: an id that names a nested run of the session is
// controlled through its route, anything else is the wrapped action's.
func nestedAwareAction(action string, inner func(sdk.Context, map[string]any) (any, error)) func(sdk.Context, map[string]any) (any, error) {
	return func(ctx sdk.Context, p map[string]any) (any, error) {
		id := firstNonEmpty(strParam(p, "runId"), strParam(p, "id"))
		if id != "" && strParam(p, "dir") == "" {
			rt := bgRT
			rt.setCtx(ctx)
			rt.mu.Lock()
			rt.state.CurrentSessionID = currentSessionID(ctx)
			target, err := resolveNestedTarget(rt.state, id)
			rt.mu.Unlock()
			if err != nil {
				return nil, mgmtError(err.Error())
			}
			if target != nil {
				switch action {
				case "interrupt":
					return nestedToolResult(interruptNestedRun(target, asyncKill, nestedControlWait))
				case "resume":
					message := jsTrim(firstNonEmpty(strParam(p, "message"), strParam(p, "task")))
					if message == "" {
						return nil, mgmtError("action='resume' requires message.")
					}
					if target.Match.Run.State == "running" || target.Match.Run.State == "queued" {
						return nestedToolResult(resumeLiveNestedRun(target, message, nestedControlWait))
					}
					if text := nestedResumeBlocked(target); text != "" {
						return nil, mgmtError(text)
					}
					// A finished nested Pi run revives through its own run tree (a launch under the nested run scope), which this port
					// does not start: refused, never routed to a top-level resume that cannot find it.
					return nil, mgmtError(refusal("reviving a finished nested run"))
				}
			}
		}
		return inner(ctx, p)
	}
}

// nestedResumeBlocked is why a finished nested run cannot be revived ("" when it can): a stopped run never, and a Pi run needs a
// session file that exists, lies under the nested run's own session directory and has its recovery identity.
func nestedResumeBlocked(t *nestedTarget) string {
	run := t.Match.Run
	if run.State == "stopped" {
		return fmt.Sprintf("Nested run %s was stopped and cannot be resumed. Start a new run instead.", run.ID)
	}
	sessionFile := run.SessionFile
	if sessionFile == "" {
		return fmt.Sprintf("Nested run %s does not have a persisted session file to resume from.", run.ID)
	}
	if _, err := os.Stat(sessionFile); err != nil {
		return fmt.Sprintf("Nested run %s session file does not exist: %s", run.ID, sessionFile)
	}
	return ""
}

func init() {
	extensionHooks = append(extensionHooks, func(*sdk.Extension) {
		if nestedWrapped {
			return
		}
		for _, name := range []string{"interrupt", "resume"} {
			if a := actions[name]; a != nil {
				a.run = nestedAwareAction(name, a.run)
			}
		}
		nestedWrapped = true
	})
}

// nestedWrapped records that the interrupt and resume actions already route nested ids (Extension() runs the hooks once per extension built).
var nestedWrapped bool
