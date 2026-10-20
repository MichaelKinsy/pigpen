package pi_subagents

import (
	"fmt"
	"path/filepath"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The steer action of the `subagent` tool (subagent-executor.ts, action === "steer") and the /subagents-steer command
// (slash-commands.ts). The action resolves its target: a workflow, a live foreground child of a workflow, a nested run or a
// background run, and sends the steer through that target's own route.

func init() {
	registerAction("steer", []string{"id", "runId", "dir", "message", "task", "mode", "index", "steeringRecovery"},
		func(ctx sdk.Context, p map[string]any) (any, error) {
			return gatedAction("steer", steerAction)(ctx, p)
		})
	extensionHooks = append(extensionHooks, func(e *sdk.Extension) {
		e.Command("subagents-steer", "Steer a live async subagent run with a message, or one child of it with --child <child-id>", subagentsSteerCommand)
	})
}

// externalRunnerControlError is the refusal of a steer (or resume) aimed at an external runner (external CLI or external job); ""
// when the run takes one. The external runner slice replaces it with the adapter's own reasons.
var externalRunnerControlError = func(asyncDir, action string) string {
	st, _ := readStatus(asyncDir)
	if st == nil {
		return ""
	}
	for i := range st.Steps {
		switch stepRunnerType(&st.Steps[i]) {
		case "external-cli", "external-job":
			if action == "steer" {
				return "External runners do not accept live steer messages."
			}
			return "External runners do not persist Pi sessions and cannot be resumed."
		}
	}
	return ""
}

func resolveSteerDeliveryMode(v any) (steerDeliveryMode, bool) {
	s, ok := v.(string)
	switch steerDeliveryMode(s) {
	case steerModeSteer, steerModeFollowUp, steerModeAuto:
		return steerDeliveryMode(s), ok
	}
	return "", false
}

// steerResult turns a reply into the tool's result: a steer receipt is a normal result, an error one carries the receipt too.
func steerResult(r steerReply) (any, error) {
	details := map[string]any{"mode": "management", "results": []any{}}
	if r.Steering != nil {
		details["steering"] = r.Steering
	}
	if r.IsError {
		if r.Steering == nil {
			return nil, mgmtError(r.Text)
		}
		return sdk.ToolResult{Content: r.Text, IsError: true, Details: details}, nil
	}
	return explicitOK{Content: r.Text, Details: details}, nil
}

func steerParams(p map[string]any) (message string, mode steerDeliveryMode, index *int, bad string) {
	if p["mode"] != nil {
		m, ok := resolveSteerDeliveryMode(p["mode"])
		if !ok {
			return "", "", nil, "action='steer' mode must be 'steer', 'follow_up', or 'auto'."
		}
		mode = m
	}
	if f, ok := p["index"].(float64); ok {
		if i, ok := floatIndex(f); ok {
			index = &i
		}
	}
	message = jsTrim(firstNonEmpty(strParam(p, "message"), strParam(p, "task")))
	return message, mode, index, ""
}

func steerAction(ctx sdk.Context, p map[string]any) (any, error) {
	rt := bgRT
	if mode := p["mode"]; mode != nil {
		if _, ok := resolveSteerDeliveryMode(mode); !ok {
			return nil, mgmtError("action='steer' mode must be 'steer', 'follow_up', or 'auto'.")
		}
	}
	rt.setCtx(ctx)
	sessionID := currentSessionID(ctx)
	rt.mu.Lock()
	rt.state.CurrentSessionID = sessionID
	rt.mu.Unlock()
	message, mode, index, _ := steerParams(p)
	if message == "" {
		return nil, mgmtError("action='steer' requires message.")
	}
	done, stop := cancellation(ctx)
	defer stop()
	cancelled := func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}
	recover := p["steeringRecovery"] != false
	targetID := firstNonEmpty(strParam(p, "runId"), strParam(p, "id"))
	if dir := strParam(p, "dir"); dir != "" {
		loc, err := resolveAsyncRunLocation(RunLocationParams{ID: strParam(p, "id"), RunID: strParam(p, "runId"), Dir: dir}, rt.dirs.Async, rt.dirs.Results)
		if err != nil {
			return nil, mgmtError(err.Error())
		}
		runID := firstNonEmpty(loc.ResolvedID, targetID)
		if runID == "" {
			runID = filepath.Base(firstNonEmpty(loc.AsyncDir, dir))
		}
		return rt.steerLocation(ctx, p, loc.AsyncDir, runID, message, mode, index, recover, cancelled)
	}
	if targetID == "" {
		return nil, mgmtError("action='steer' requires id or dir.")
	}
	// A live foreground control, exactly addressed, then the nested and async runs.
	if c := rt.state.ForegroundControls[targetID]; c != nil {
		target, msg := resolveWorkflowForegroundSteeringTarget(rt.state, targetID, "", rt.dirs.Async)
		if target == nil {
			return nil, mgmtError(msg)
		}
		return steerResult(steerWorkflowForegroundTarget(target, message, mode, index))
	}
	resolved, err := resolveSubagentRunID(targetID, rt.state, rt.dirs.Async, rt.dirs.Results)
	if err != nil {
		return nil, mgmtError(err.Error())
	}
	if resolved == nil {
		if nested, handled := steerNestedAction(rt, targetID, message, mode, index, cancelled); handled {
			return steerResult(nested)
		}
		return nil, mgmtError(fmt.Sprintf("No async run found for '%s'.", targetID))
	}
	if resolved.Kind != "async" {
		return nil, mgmtError(fmt.Sprintf("No async run found for '%s'.", targetID))
	}
	return rt.steerLocation(ctx, p, resolved.Location.AsyncDir, resolved.ID, message, mode, index, recover, cancelled)
}

// steerLocation steers the run in asyncDir: a workflow through its own route, a background run through its steer inbox.
func (rt *asyncRuntime) steerLocation(ctx sdk.Context, p map[string]any, asyncDir, runID, message string, mode steerDeliveryMode, index *int, recover bool, cancelled func() bool) (any, error) {
	if asyncDir != "" {
		if st, _ := readStatus(asyncDir); st != nil && st.Mode == "workflow" {
			in := steerWorkflowRunInput{State: rt.state, RunID: runID, AsyncDir: asyncDir, Message: message, Mode: mode, Cancelled: cancelled}
			if f, ok := p["index"].(float64); ok {
				in.Index = &f
			}
			return steerResult(steerWorkflowRun(in))
		}
		if msg := externalRunnerControlError(asyncDir, "steer"); msg != "" {
			return nil, mgmtError(msg)
		}
	}
	in := steerRunInput{CurrentSessionID: currentSessionID(ctx), RunID: runID, Message: message, Mode: mode, Index: index, AsyncDir: asyncDir, Dirs: rt.dirs,
		ResultsDir: rt.dirs.Results, Kill: asyncKill, Cancelled: cancelled, FindPendingAsks: supervisorFindPendingAsks(rt.state)}
	in.Touch = func() {
		rt.mu.Lock()
		if j := rt.state.AsyncJobs[runID]; j != nil {
			j.UpdatedAt = nowMs()
		}
		rt.mu.Unlock()
	}
	if recover {
		in.Recover = func(l steeringRecoveryLimits) steerRecoverResult {
			return rt.recoverSteeredRun(ctx, runID, message, l)
		}
	}
	return steerResult(steerAsyncRun(in))
}

// recoverSteeredRun launches the replacement of a paused source run: a resume with the steer as its follow-up and only the limits
// the source had left.
func (rt *asyncRuntime) recoverSteeredRun(ctx sdk.Context, runID, message string, l steeringRecoveryLimits) steerRecoverResult {
	if l.ToolBudget != nil {
		return steerRecoverResult{Error: refusal("recovering a run that carries a tool budget")}
	}
	p := map[string]any{"action": "resume", "id": runID, "message": message}
	if l.TimeoutMs != nil {
		p["timeoutMs"] = float64(*l.TimeoutMs)
	}
	res, err := rt.resumeAction(ctx, p)
	if err != nil {
		return steerRecoverResult{Error: err.Error()}
	}
	if ok, isOK := res.(explicitOK); isOK {
		if d, _ := ok.Details.(map[string]any); d != nil {
			if id, _ := d["asyncId"].(string); id != "" {
				return steerRecoverResult{AsyncID: id}
			}
		}
	}
	return steerRecoverResult{}
}

// subagentsSteerCommand is `/subagents-steer <run-id> [--child <child-id>] <message>`: only the optional child selector is a flag;
// everything after it is the message, whatever it looks like.
func subagentsSteerCommand(ctx sdk.Context, args string) error {
	usage := "Usage: /subagents-steer <run-id> [--child <child-id>] <message>"
	tokens := strings.Fields(args)
	send := func(text string) error { slashText(ctx, text); return nil }
	if len(tokens) == 0 || strings.HasPrefix(tokens[0], "--") {
		return send(usage)
	}
	id, rest := tokens[0], tokens[1:]
	childID, start := "", 0
	if len(rest) >= 2 && rest[0] == "--child" && !strings.HasPrefix(rest[1], "--") {
		childID, start = rest[1], 2
	}
	message := jsTrim(strings.Join(rest[start:], " "))
	if message == "" {
		return send(usage)
	}
	rt := bgRT
	rt.setCtx(ctx)
	sessionID := currentSessionID(ctx)
	rt.mu.Lock()
	rt.state.CurrentSessionID = sessionID
	rt.mu.Unlock()
	targetID := id
	var childIndex *int
	if childID != "" {
		dir := filepath.Join(rt.dirs.Async, id)
		st, _ := readStatus(dir)
		if st == nil || (sessionID != "" && st.SessionID != sessionID) || !(st.State == "queued" || st.State == "running" || st.State == "paused") {
			return send(fmt.Sprintf("No current-session async run found for '%s'.", id))
		}
		var matches []int
		for i, step := range st.Steps {
			for _, c := range asyncStatusChildIdentityCandidates(step, i) {
				if c == childID {
					matches = append(matches, i)
					break
				}
			}
		}
		switch {
		case len(matches) > 1:
			return send(fmt.Sprintf("Child '%s' is ambiguous under async run '%s'.", childID, st.RunID))
		case len(matches) == 0:
			if nestedID := nestedChildOf(rt, st.RunID, childID); nestedID != "" {
				targetID = nestedID
				break
			}
			return send(fmt.Sprintf("Child '%s' was not found under async run '%s'.", childID, st.RunID))
		default:
			if step := st.Steps[matches[0]]; step.RunID != "" {
				targetID = step.RunID
			} else {
				childIndex = &matches[0]
			}
		}
	}
	// Host-style exact-child authority, as the extension RPC's non-recovering steer: a missed acknowledgment never swaps the
	// addressed child for a pause-and-revive replacement.
	params := map[string]any{"action": "steer", "id": targetID, "message": message, "steeringRecovery": false}
	if childIndex != nil {
		params["index"] = float64(*childIndex)
	}
	res, err := executeSubagent(ctx, params)
	return reportSlashResult(ctx, res, err)
}

// reportSlashResult shows the result of the tool a slash command ran.
func reportSlashResult(ctx sdk.Context, res any, err error) error {
	switch r := res.(type) {
	case explicitOK:
		slashText(ctx, r.Content)
		return nil
	case sdk.ToolResult:
		slashText(ctx, r.Content)
		return nil
	}
	if err != nil {
		slashText(ctx, err.Error())
		return nil
	}
	return nil
}
