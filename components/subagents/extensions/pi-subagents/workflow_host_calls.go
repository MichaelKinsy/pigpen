package pi_subagents

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

func sortStrings(s []string) { sort.Strings(s) }

func (h *wfHost) pollAbort() {
	if h.opt.Ctx != nil && h.opt.Ctx.Err() != nil && !h.abortSeen && !h.finishing && !h.settled {
		h.onAbort()
	}
}

func jsErrString(err error) string { return err.Error() }

func recoveryBarrierMessage(sourceKey, target string) string {
	return "Run '" + target + "' cannot launch after run '" + sourceKey + "' returned rejected acceptance recovery; only explicit read-only review children with acceptance:false may follow."
}

func isAcceptanceMetadataRecovery(result *jsObject) bool {
	if objBool(result, "ok") {
		return false
	}
	rec := result.obj("recovery")
	return rec != nil && objStr(rec, "status") == "available-for-review" && objStr(rec, "reason") == "acceptance-metadata-rejected"
}

func (h *wfHost) assertRecoveryBarrierAllowsRun(key string, params *jsObject) error {
	if h.recoveryBarrier == nil || isExplicitReadOnlyRecoveryReview(params) {
		return nil
	}
	return errors.New(recoveryBarrierMessage(*h.recoveryBarrier, key))
}

func workflowStringMetadata(params *jsObject) (phase, label, agent string) {
	pick := func(k string) string {
		if s, ok := params.vals[k].(string); ok && jsTrim(s) != "" {
			return jsTrim(s)
		}
		return ""
	}
	return pick("phase"), pick("label"), pick("agent")
}

func traceWithMetadata(e WorkflowScriptTraceEntry, params *jsObject) WorkflowScriptTraceEntry {
	e.Phase, e.Label, e.Agent = "", "", ""
	phase, label, agent := workflowStringMetadata(params)
	if e.Phase == "" {
		e.Phase = phase
	}
	e.Label = label
	e.Agent = agent
	return e
}

// respondValue answers a call with a value (the "response" message, scripted-workflow.ts:2383).
func (h *wfHost) respondFuture(callID int, f *WorkflowFuture[any], responsePath string, onBoundaryError func(error)) {
	onSettle(h.loop, f, func(v any, err error) {
		if h.settled {
			return
		}
		if err != nil {
			kind := workflowErrorKind(err)
			if !isWorkflowFailureKind(kind) {
				kind = ""
			}
			h.postResponse(callID, false, nil, err.Error(), kind)
			return
		}
		h.respondValue(callID, v, responsePath, onBoundaryError)
	})
}

func (h *wfHost) respondValue(callID int, v any, responsePath string, onBoundaryError func(error)) {
	if responsePath == "" {
		h.postResponse(callID, true, omitUndefinedGo(v), "", "")
		return
	}
	normalized := omitNonJSONChildMetadata(v)
	if err := assertWorkflowJSONValue(normalized, responsePath, map[any]bool{}); err != nil {
		if onBoundaryError != nil {
			onBoundaryError(err)
		}
		h.postResponse(callID, false, nil, responsePath+" must contain only JSON data before it can be returned from workflowScript. Return a plain projection such as { runId, ok, output }. "+err.Error(), "")
		return
	}
	h.postResponse(callID, true, normalized, "", "")
}

func (h *wfHost) respondRejected(callID int, err error) {
	h.respondFuture(callID, WorkflowRejected[any](err), "", nil)
}

func (h *wfHost) postResponse(callID int, ok bool, value any, errMsg, kind string) {
	h.sbQ.post(func() { h.sb.respond(callID, ok, value, errMsg, kind) })
}

// onCall handles the script's calls (scripted-workflow.ts:2380-2701).
func (h *wfHost) onCall(callID int, method string, args *jsObject) {
	if h.settled {
		return
	}
	if h.assemblyAbortRequested {
		err := h.abortError
		if err == nil {
			err = errors.New("Workflow context was replaced or reloaded.")
		}
		h.finish(wfOutcome{err: err})
		return
	}
	switch method {
	case "state.get", "state.set":
		h.onState(callID, method, args)
	case "status":
		h.onStatus(callID, args)
	case "steer":
		h.onSteer(callID, args)
	case "host":
		h.onHost(callID, args)
	case "run":
		h.onRun(callID, args)
	default:
		h.respondRejected(callID, fmt.Errorf("Unknown runs API method '%s'.", method))
	}
}

func workflowValidateKey(v any, owner string) (string, error) {
	s, ok := v.(string)
	if !ok || !workflowKeyPattern.MatchString(s) {
		return "", fmt.Errorf("%s key must be 1-128 characters using letters, numbers, '.', '_' or '-', and start with a letter or number.", owner)
	}
	return s, nil
}

func (h *wfHost) onState(callID int, method string, args *jsObject) {
	if h.opt.State == nil {
		h.respondRejected(callID, errors.New("Workflow state is unavailable without a mission."))
		return
	}
	key, err := workflowValidateKey(args.vals["key"], "state")
	if err != nil {
		h.respondRejected(callID, err)
		return
	}
	if method == "state.get" {
		h.respondFuture(callID, workflowThenF(h.loop, WorkflowResolved(struct{}{}), func(struct{}, error) *WorkflowFuture[any] { return h.opt.State.Get(key) }), "", nil)
		return
	}
	if h.recoveryBarrier != nil {
		h.respondRejected(callID, errors.New(recoveryBarrierMessage(*h.recoveryBarrier, "state.set('"+key+"')")))
		return
	}
	value := args.vals["value"]
	if err := assertWorkflowJSONValue(value, "state.set('"+key+"') value", map[any]bool{}); err != nil {
		h.respondRejected(callID, err)
		return
	}
	f := workflowThenF(h.loop, WorkflowResolved(struct{}{}), func(struct{}, error) *WorkflowFuture[struct{}] { return h.opt.State.Set(key, value) })
	h.respondFuture(callID, workflowThen(h.loop, f, func(_ struct{}, err error) (any, error) { return undef{}, err }), "", nil)
}

// asAny widens a child-result future for respondFuture.
func asAny(l *workflowLoop, f *WorkflowFuture[*jsObject]) *WorkflowFuture[any] {
	return workflowThen(l, f, func(v *jsObject, err error) (any, error) {
		if err != nil {
			return nil, err
		}
		if v == nil {
			return nil, nil
		}
		return v, nil
	})
}

func (h *wfHost) onStatus(callID int, args *jsObject) {
	keyOrRunID, ok := args.vals["keyOrRunId"].(string)
	if !ok || jsTrim(keyOrRunID) == "" {
		h.respondRejected(callID, errors.New("runs.status(keyOrRunId) requires a non-empty string."))
		return
	}
	known := h.children[keyOrRunID]
	target := keyOrRunID
	if id := objStr(known, "runId"); id != "" {
		target = id
	}
	e := WorkflowScriptTraceEntry{Operation: "status", Key: keyOrRunID, State: "started"}
	if id := objStr(known, "runId"); id != "" {
		e.RunID = id
	}
	h.trace = append(h.trace, e)
	h.traceChanged()
	h.pollAbort()
	if h.settled || h.finishing {
		return
	}
	f := workflowThen(h.loop, h.opt.Status(h.childCtx, target), func(result *jsObject, err error) (*jsObject, error) {
		if err != nil {
			return nil, err
		}
		if h.settled || h.finishing {
			return result, nil
		}
		ok := objBool(result, "ok")
		state := "completed"
		if !ok {
			state = "failed"
		}
		te := WorkflowScriptTraceEntry{Operation: "status", Key: keyOrRunID, State: state, RunID: objStr(result, "runId")}
		if !ok {
			te.Error = objStr(result, "output")
		}
		h.trace = append(h.trace, te)
		h.traceChanged()
		if !ok {
			return nil, fmt.Errorf("Status '%s' failed: %s", keyOrRunID, objStr(result, "output"))
		}
		return result, nil
	})
	h.respondFuture(callID, asAny(h.loop, f), "", nil)
}

func (h *wfHost) onSteer(callID int, args *jsObject) {
	key, err := workflowValidateKey(args.vals["key"], "runs.steer")
	if err != nil {
		h.respondRejected(callID, err)
		return
	}
	msg, ok := args.vals["message"].(string)
	if !ok || jsTrim(msg) == "" {
		h.respondRejected(callID, fmt.Errorf("runs.steer('%s') requires a non-empty message.", key))
		return
	}
	var opts WorkflowSteerOptions
	if o, ok := args.vals["options"].(*jsObject); ok {
		opts.Mode, _ = o.vals["mode"].(string)
		if f, ok := o.vals["index"].(float64); ok {
			opts.Index = new(int(f))
		}
		if f, ok := o.vals["ackTimeoutMs"].(float64); ok {
			opts.AckTimeoutMs = new(int(f))
		}
	}
	if h.recoveryBarrier != nil {
		h.respondRejected(callID, errors.New(recoveryBarrierMessage(*h.recoveryBarrier, "runs.steer('"+key+"')")))
		return
	}
	startedAt := time.Now()
	h.trace = append(h.trace, WorkflowScriptTraceEntry{Operation: "steer", Key: key, State: "started"})
	h.traceChanged()
	step := workflowThenF(h.loop, WorkflowResolved(struct{}{}), func(struct{}, error) *WorkflowFuture[WorkflowSteerResult] {
		if _, launched := h.launches[key]; !launched {
			return WorkflowRejected[WorkflowSteerResult](fmt.Errorf("runs.steer('%s') requires a prior runs.run/runs.all launch with that key.", key))
		}
		if h.opt.Steer == nil {
			return WorkflowRejected[WorkflowSteerResult](errors.New("Workflow steering is unavailable in this host."))
		}
		return h.opt.Steer(h.childCtx, key, jsTrim(msg), opts)
	})
	promise := workflowThen(h.loop, step, func(receipt WorkflowSteerResult, err error) (*jsObject, error) {
		if err != nil {
			h.trace = append(h.trace, WorkflowScriptTraceEntry{Operation: "steer", Key: key, State: "failed", DurationMs: new(time.Since(startedAt).Milliseconds()), Error: err.Error()})
			h.traceChanged()
			return nil, err
		}
		h.trace = append(h.trace, WorkflowScriptTraceEntry{Operation: "steer", Key: key, State: receipt.State, DurationMs: new(time.Since(startedAt).Milliseconds()), Error: receipt.Error})
		h.traceChanged()
		return receipt.object(), nil
	})
	h.steers[callID] = &wfSteerCall{key: key, promise: promise, observed: h.observedSteerCalls[callID]}
	delete(h.observedSteerCalls, callID)
	h.steerOrder = append(h.steerOrder, callID)
	h.respondFuture(callID, asAny(h.loop, promise), "", nil)
}

var workflowHostOutputUnsafe = regexp.MustCompile(`[\x00-\x1f\x7f]`)

// normalizeWorkflowHostCommandParams is normalizeWorkflowHostCommandParams (host-command.ts:44).
func normalizeWorkflowHostCommandParams(value any, label string) (WorkflowHostCommandParams, error) {
	var zero WorkflowHostCommandParams
	p, ok := value.(*jsObject)
	if !ok {
		return zero, fmt.Errorf("%s must be an object.", label)
	}
	var unknown []string
	for _, k := range p.order() {
		if !workflowHostAllowed[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		hint := ""
		for _, u := range unknown {
			if u == "cwd" {
				hint = workflowHostCwdHint
			}
		}
		return zero, fmt.Errorf("%s has unsupported fields: %s.%s", label, strings.Join(unknown, ", "), hint)
	}
	if k, _ := p.vals["kind"].(string); k != "command" {
		return zero, fmt.Errorf("%s.kind must be 'command'.", label)
	}
	cmd, ok := p.vals["command"].(string)
	if !ok || jsTrim(cmd) == "" {
		return zero, fmt.Errorf("%s.command must be a non-empty string.", label)
	}
	command := jsTrim(cmd)
	if len(command) > 16*1024 || strings.Contains(command, "\x00") {
		return zero, fmt.Errorf("%s.command exceeds the %d-byte limit or contains NUL.", label, 16*1024)
	}
	to, ok := p.vals["timeoutMs"].(float64)
	if !ok || to != float64(int64(to)) || to < 1 || to > 24*60*60*1000 {
		return zero, fmt.Errorf("%s.timeoutMs must be an integer from 1 to %d.", label, 24*60*60*1000)
	}
	out := WorkflowHostCommandParams{Command: command, TimeoutMs: int(to)}
	if v, has := p.vals["output"]; has {
		if _, u := v.(undef); !u {
			s, ok := v.(string)
			if !ok || jsTrim(s) == "" {
				return zero, fmt.Errorf("%s.output must be a non-empty relative path.", label)
			}
			output := jsTrim(s)
			abs := strings.HasPrefix(output, "/") || regexp.MustCompile(`^[A-Za-z]:[\\/]`).MatchString(output) || strings.HasPrefix(output, `\\`)
			trav := false
			for _, seg := range regexp.MustCompile(`[\\/]`).Split(output, -1) {
				if seg == ".." {
					trav = true
				}
			}
			if abs || trav || workflowHostOutputUnsafe.MatchString(output) || len(output) > 240 {
				return zero, fmt.Errorf("%s.output must be a bounded relative path without traversal or control characters.", label)
			}
			out.Output = output
		}
	}
	if v, has := p.vals["role"]; has {
		if _, u := v.(undef); !u {
			if r, _ := v.(string); r != "ci" && r != "gate" {
				return zero, fmt.Errorf("%s.role must be 'ci' or 'gate'.", label)
			}
			out.Role = v.(string)
		}
	}
	if v, has := p.vals["provider"]; has {
		if _, u := v.(undef); !u {
			s, ok := v.(string)
			if !ok || jsTrim(s) == "" || strings.ContainsAny(s, "\r\n\x00") || len(jsTrim(s)) > 64 {
				return zero, fmt.Errorf("%s.provider must be a non-empty single-line string of at most 64 bytes.", label)
			}
			out.Provider = jsTrim(s)
		}
	}
	return out, nil
}

var workflowWhitespaceRuns = regexp.MustCompile(`\s+`)

func (h *wfHost) onHost(callID int, args *jsObject) {
	key, err := workflowValidateKey(args.vals["key"], "runs.host")
	var params WorkflowHostCommandParams
	if err == nil {
		params, err = normalizeWorkflowHostCommandParams(args.vals["params"], "runs.host('"+key+"') params")
	}
	if err != nil {
		h.respondRejected(callID, err)
		return
	}
	if h.recoveryBarrier != nil {
		h.respondRejected(callID, errors.New(recoveryBarrierMessage(*h.recoveryBarrier, "runs.host('"+key+"')")))
		return
	}
	if h.opt.Host == nil {
		h.respondRejected(callID, errors.New("runs.host is unavailable in this host context."))
		return
	}
	if len(h.hostCalls) >= 32 {
		h.respondRejected(callID, errors.New("workflowScript supports at most 32 runs.host calls."))
		return
	}
	started := time.Now()
	startedMs := started.UnixMilli()
	startedStep := WorkflowHostStepNode{ID: key, Label: key, Role: params.Role, Provider: params.Provider, State: "running", UpdatedAt: startedMs, DeadlineAt: startedMs + int64(params.TimeoutMs)}
	h.hostStepChanged(startedStep)
	h.trace = append(h.trace, WorkflowScriptTraceEntry{Operation: "host", Key: key, State: "started"})
	h.traceChanged()
	step := workflowThenF(h.loop, WorkflowResolved(struct{}{}), func(struct{}, error) *WorkflowFuture[WorkflowHostCommandResult] {
		return h.opt.Host(h.childCtx, key, params)
	})
	promise := workflowThen(h.loop, step, func(result WorkflowHostCommandResult, err error) (*jsObject, error) {
		if err != nil {
			text := err.Error()
			s := startedStep
			s.State, s.ReasonCode = "error", "execution_failed"
			s.Detail = string(utf16Slice(workflowWhitespaceRuns.ReplaceAllString(text, " "), 200))
			s.UpdatedAt = time.Now().UnixMilli()
			h.hostStepChanged(s)
			h.trace = append(h.trace, WorkflowScriptTraceEntry{Operation: "host", Key: key, State: "failed", DurationMs: new(time.Since(started).Milliseconds()), Error: text})
			h.traceChanged()
			return nil, err
		}
		state := "error"
		switch {
		case result.State == "stopped":
			state = "cancelled"
		case result.OK:
			state = "done"
		}
		var parts []string
		if result.Error != "" {
			parts = append(parts, result.Error)
		}
		if t := jsTrim(result.Stderr); t != "" {
			parts = append(parts, t)
		} else if t := jsTrim(result.Stdout); t != "" {
			parts = append(parts, t)
		}
		detail := jsTrim(workflowWhitespaceRuns.ReplaceAllString(strings.Join(parts, " "), " "))
		detail = string(utf16Slice(detail, 200))
		s := startedStep
		s.State = state
		if state == "done" {
			s.Verdict = "pass"
		}
		if !result.OK {
			switch {
			case result.State == "timed-out":
				s.ReasonCode = "timed_out"
			case result.State == "stopped":
				s.ReasonCode = "aborted"
			default:
				s.ReasonCode = "command_failed"
			}
		}
		s.Detail = detail
		s.ReportPath = params.Output
		if s.ReportPath == "" {
			segs := regexp.MustCompile(`[\\/]`).Split(result.OutputPath, -1)
			s.ReportPath = segs[len(segs)-1]
		}
		s.ExitCode = result.ExitCode
		s.UpdatedAt = time.Now().UnixMilli()
		h.hostStepChanged(s)
		te := WorkflowScriptTraceEntry{Operation: "host", Key: key, State: "failed", DurationMs: new(result.DurationMs)}
		switch {
		case result.OK:
			te.State = "completed"
		case result.State == "stopped":
			te.State = "stopped"
		}
		if !result.OK {
			te.Error = result.Error
			if te.Error == "" {
				te.Error = "Host command failed."
			}
		}
		h.trace = append(h.trace, te)
		h.traceChanged()
		if !result.OK {
			d := detail
			if d == "" {
				d = result.Error
			}
			if d == "" {
				if result.ExitCode != nil {
					d = fmt.Sprintf("exit code %d", *result.ExitCode)
				} else {
					d = "exit code unknown"
				}
			}
			return nil, fmt.Errorf("Host command '%s' failed: %s", key, d)
		}
		return result.object(), nil
	})
	h.hostCalls[callID] = &wfHostCall{key: key, promise: promise, observed: h.observedHostCalls[callID]}
	delete(h.observedHostCalls, callID)
	h.hostOrder = append(h.hostOrder, callID)
	h.respondFuture(callID, asAny(h.loop, promise), "runs.host('"+key+"') result", nil)
}

type wfLaunchPlan struct{}

// onRun launches one child (scripted-workflow.ts:2523-2701).
func (h *wfHost) onRun(callID int, args *jsObject) {
	key, err := workflowValidateKey(args.vals["key"], "runs.run")
	if err != nil {
		h.respondRejected(callID, err)
		return
	}
	params, ok := args.vals["params"].(*jsObject)
	if !ok {
		h.respondRejected(callID, fmt.Errorf("runs.run('%s', params) requires a params object.", key))
		return
	}
	generatedLaneKey := ""
	if g, ok := args.vals["generatedLaneKey"].(string); ok && workflowKeyPattern.MatchString(g) && strings.HasPrefix(key, g+".") {
		generatedLaneKey = g
	}
	collectFailure, _ := args.vals["collectFailure"].(bool)
	callObserved := h.observedRunCalls[callID]
	delete(h.observedRunCalls, callID)

	deliver := func(f *WorkflowFuture[*jsObject]) *WorkflowFuture[*jsObject] {
		if collectFailure {
			return f
		}
		return workflowThen(h.loop, f, func(result *jsObject, err error) (*jsObject, error) {
			if err != nil {
				return nil, err
			}
			rec := result.obj("recovery")
			recoverable := rec != nil && objStr(rec, "status") == "available-for-review" && objStr(rec, "reason") == "acceptance-metadata-rejected"
			if !objBool(result, "ok") && objStr(result, "state") != "running" && !objTruthy(result, "stopped") && !recoverable {
				detached := objTruthy(result, "detached")
				verb, kind := "failed", WorkflowFailureChild
				if detached {
					verb, kind = "detached", WorkflowFailureDetachedChild
				}
				msg, hasErr := objHasString(result, "error")
				if !hasErr || result.vals["error"] == nil {
					msg = nonNilString(result, "output")
				}
				return nil, taggedWorkflowError(fmt.Sprintf("Run '%s' %s: %s", key, verb, msg), kind)
			}
			return result, nil
		})
	}

	fingerprint := stableRunJSON(canonicalRunParams(params))
	if existing := h.launches[key]; existing != nil {
		if existing.fingerprint != fingerprint {
			h.respondRejected(callID, fmt.Errorf("Duplicate workflow key '%s' used with incompatible launch params.", key))
			return
		}
		if callObserved {
			existing.observed = true
		}
		e := WorkflowScriptTraceEntry{Operation: "run", Key: key, State: "reused", GeneratedLaneKey: existing.generatedLaneKey}
		e.Phase, e.Label, e.Agent = workflowStringMetadata(params)
		h.trace = append(h.trace, e)
		h.traceChanged()
		h.respondFuture(callID, asAny(h.loop, deliver(existing.promise)), "runs.run('"+key+"') result", func(err error) { h.children[key] = responseBoundaryFailure(key, err) })
		return
	}
	if p := h.opt.OneUsePermit; p != nil {
		if msg := p.Claim(key); msg != "" {
			h.respondRejected(callID, errors.New(msg))
			return
		}
		if _, batched := args.vals["batch"]; batched {
			h.respondRejected(callID, errors.New("Workflow child permit does not support runs.all."))
			return
		}
		if _, has := params.vals["resume"]; has {
			h.respondRejected(callID, errors.New("Workflow child permit does not support retained resume."))
			return
		}
	}
	reject := func(format string, a ...any) {
		h.respondRejected(callID, fmt.Errorf(format, a...))
	}
	has := func(k string) bool {
		v, ok := params.vals[k]
		if !ok {
			return false
		}
		_, u := v.(undef)
		return !u
	}
	if has("action") {
		reject("runs.run('%s') accepts execution params only; management action is not allowed.", key)
		return
	}
	if has("workflowScript") {
		reject("runs.run('%s') cannot start a nested workflow script.", key)
		return
	}
	if has("tasks") || has("chain") || has("parallel") || has("concurrency") || has("chainDir") {
		reject("runs.run('%s') accepts one child via { agent, task }; use runs.all(...) and JavaScript control flow for orchestration.", key)
		return
	}
	if has("worktree") {
		if _, isBool := params.vals["worktree"].(bool); !isBool {
			reject("runs.run('%s') worktree must be true or false.", key)
			return
		}
	}
	if has("baseRef") {
		if s, isStr := params.vals["baseRef"].(string); !isStr || !validGitRef(s) {
			reject("runs.run('%s') %s", key, workflowBaseRefValidationError)
			return
		}
	}
	if has("gate") {
		if msg := workflowGateShapeErrorGo(params.vals["gate"]); msg != "" {
			reject("runs.run('%s') %s", key, msg)
			return
		}
	}
	if has("gate") && has("acceptance") && params.vals["acceptance"] != false {
		reject("runs.run('%s') gate cannot be combined with acceptance; use one gate command or acceptance.verify.%s", key, workflowGateAcceptanceConflictGo(params.vals["gate"], params.vals["acceptance"]))
		return
	}
	if has("gate") && has("resume") {
		reject("runs.run('%s') gate is not supported with retained resume.", key)
		return
	}
	var resumeRef *WorkflowResumeRef
	if has("resume") {
		if _, isStr := params.vals["resume"].(string); !isStr {
			ref, msg := parseWorkflowResumeReference(params.vals["resume"])
			if msg != "" {
				reject("runs.run('%s') %s", key, msg)
				return
			}
			resumeRef = ref
		}
	}
	if s, isStr := params.vals["resume"].(string); isStr && jsTrim(s) == "" {
		reject("runs.run('%s') resume must be a non-empty retained run id.", key)
		return
	}
	if has("resume") && has("agent") {
		reject("runs.run('%s') resume and agent are mutually exclusive.", key)
		return
	}
	if has("resume") {
		if t, ok := params.vals["task"].(string); !ok || jsTrim(t) == "" {
			reject("runs.run('%s') resume requires a non-empty task follow-up.", key)
			return
		}
	}

	startedAt := time.Now()
	var batchID string
	var batchCalls []WorkflowCallSpec
	hasBatch := false
	if b, ok := args.vals["batch"].(*jsObject); ok {
		if id, ok := b.vals["id"].(string); ok {
			if calls, ok := b.vals["calls"].([]any); ok {
				hasBatch = true
				batchID = id
				for _, c := range calls {
					co, ok := c.(*jsObject)
					if !ok {
						continue
					}
					k, isStr := co.vals["key"].(string)
					p, isObj := co.vals["params"].(*jsObject)
					if isStr && isObj {
						batchCalls = append(batchCalls, WorkflowCallSpec{Key: k, Params: p})
					}
				}
			}
		}
	}
	var admission *WorkflowFuture[struct{}]
	if hasBatch {
		admission = h.batchAdmissions[batchID]
	}
	if admission == nil {
		seen := map[string]bool{}
		src := batchCalls
		if !hasBatch {
			src = []WorkflowCallSpec{{Key: key, Params: params}}
		}
		var calls []WorkflowCallSpec
		for _, c := range src {
			if seen[c.Key] || h.launches[c.Key] != nil {
				continue
			}
			seen[c.Key] = true
			calls = append(calls, c)
		}
		admission = workflowThenF(h.loop, WorkflowResolved(struct{}{}), func(struct{}, error) *WorkflowFuture[struct{}] {
			if h.settled || h.finishing {
				return WorkflowResolved(struct{}{})
			}
			for _, c := range calls {
				if err := h.assertRecoveryBarrierAllowsRun(c.Key, c.Params); err != nil {
					return WorkflowRejected[struct{}](err)
				}
			}
			if h.opt.Admit == nil {
				return WorkflowResolved(struct{}{})
			}
			f := h.opt.Admit(h.childCtx, calls)
			h.pollAbort()
			return f
		})
		if hasBatch {
			h.batchAdmissions[batchID] = admission
		}
	}

	var lineage []string
	// first: admission settles; then resume resolution, the concurrency slot, the launch itself.
	first := workflowThenF(h.loop, admission, func(_ struct{}, aerr error) *WorkflowFuture[*jsObject] {
		if aerr != nil {
			if h.childCtx.Err() == nil {
				return WorkflowRejected[*jsObject](aerr)
			}
			return WorkflowResolved(stoppedChildResult(key, h.abortReasonText(h.childCtx)))
		}
		if h.settled || h.finishing || h.stoppedLaunches[key] {
			text := ""
			if c := h.children[key]; c != nil {
				text, _ = c.vals["error"].(string)
			}
			if text == "" {
				text = h.abortReasonText(h.childCtx)
			}
			return WorkflowResolved(stoppedChildResult(key, text))
		}
		stopCtx, stopCancel := context.WithCancelCause(context.Background())
		h.stopControllers[key] = stopCancel
		childSignal, childSignalCancel := combinedAbortContext(h.childCtx, stopCtx)
		var resumeInput *WorkflowResumeRef
		switch {
		case resumeRef != nil:
			resumeInput = resumeRef
		case h.opt.ResolveResume != nil:
			if s, ok := params.vals["resume"].(string); ok {
				resumeInput = &WorkflowResumeRef{RunID: s}
			}
		}
		var resolved *WorkflowFuture[*WorkflowResolvedResume]
		if resumeInput == nil {
			resolved = WorkflowResolved[*WorkflowResolvedResume](nil)
		} else {
			resolved = workflowThenF(h.loop, WorkflowResolved(struct{}{}), func(struct{}, error) *WorkflowFuture[*WorkflowResolvedResume] {
				if h.opt.ResolveResume == nil {
					return WorkflowRejected[*WorkflowResolvedResume](errors.New("Keyed workflow receipt resume is unavailable in this host."))
				}
				var index *int
				if f, ok := params.vals["index"].(float64); ok {
					index = new(int(f))
				}
				return workflowThen(h.loop, h.opt.ResolveResume(childSignal, *resumeInput, index), func(v WorkflowResolvedResume, err error) (*WorkflowResolvedResume, error) {
					if err != nil {
						return nil, err
					}
					return &v, nil
				})
			})
		}
		return workflowThenF(h.loop, resolved, func(rv *WorkflowResolvedResume, rerr error) *WorkflowFuture[*jsObject] {
			if rerr != nil {
				childSignalCancel()
				return WorkflowRejected[*jsObject](rerr)
			}
			resolvedID := ""
			if rv != nil {
				resolvedID = jsTrim(rv.RunID)
			}
			if resumeRef != nil && resolvedID == "" {
				childSignalCancel()
				return WorkflowRejected[*jsObject](errors.New("Keyed workflow receipt resume resolved without a retained run id."))
			}
			if rv != nil && rv.Object {
				var l []string
				for _, id := range rv.RunIDs {
					if t := jsTrim(id); t != "" {
						l = append(l, t)
					}
				}
				if len(l) == 0 {
					l = []string{resolvedID}
				}
				lineage = dedupeStrings(l)
				if lineage[len(lineage)-1] != resolvedID {
					lineage = append(lineage, resolvedID)
				}
				if resumeInput != nil && resumeInput.RunID != "" {
					for _, c := range h.children {
						if objStr(c, "runId") == resolvedID {
							if cont := c.obj("continuation"); cont != nil {
								if ids, ok := cont.vals["runIds"].([]any); ok && len(ids) > 0 && ids[len(ids)-1] == resolvedID {
									lineage = nil
									for _, id := range ids {
										lineage = append(lineage, id.(string))
									}
								}
							}
							break
						}
					}
				}
			}
			launchParams := params
			if resolvedID != "" {
				launchParams = params.clone()
				launchParams.set("resume", resolvedID)
			}
			return workflowThenF(h.loop, h.launchSem.Acquire(), func(struct{}, error) *WorkflowFuture[*jsObject] {
				release := func() { h.launchSem.Release(); childSignalCancel() }
				if h.settled || h.finishing || h.stoppedLaunches[key] || childSignal.Err() != nil {
					text := ""
					if c := h.children[key]; c != nil {
						text, _ = c.vals["error"].(string)
					}
					if text == "" {
						text = h.abortReasonText(childSignal)
					}
					release()
					return WorkflowResolved(stoppedChildResult(key, text))
				}
				launched := h.opt.Launch(childSignal, key, launchParams, WorkflowAdmission{Admitted: true, Batch: hasBatch})
				h.pollAbort()
				return workflowThenF(h.loop, launched, func(result *jsObject, lerr error) *WorkflowFuture[*jsObject] {
					if lerr != nil {
						release()
						return WorkflowRejected[*jsObject](lerr)
					}
					auto := setupAbortResumeParams(params, result, childSignal)
					if auto == nil {
						release()
						return WorkflowResolved(result)
					}
					lineage = dedupeStrings(append(append([]string(nil), lineage...), objStr(result, "runId")))
					ae := WorkflowScriptTraceEntry{Operation: "run", Key: key, State: "started", GeneratedLaneKey: generatedLaneKey}
					ae.Phase, ae.Label, ae.Agent = workflowStringMetadata(auto)
					ae.Phase = "auto-resume"
					ae.RunID = objStr(result, "runId")
					h.trace = append(h.trace, ae)
					h.traceChanged()
					f := h.opt.Launch(childSignal, key, auto, WorkflowAdmission{Admitted: true, Batch: hasBatch})
					return workflowThen(h.loop, f, func(r *jsObject, err error) (*jsObject, error) {
						release()
						return r, err
					})
				})
			})
		})
	})
	_ = first

	// the admission rejection handler of the original is the aerr branch above; what follows records the outcome.
	recorded := workflowThen(h.loop, first, func(result *jsObject, err error) (*jsObject, error) {
		if err != nil {
			text := err.Error()
			failure := newObject()
			failure.set("key", key)
			failure.set("ok", false)
			failure.set("output", text)
			failure.set("error", text)
			failure.set("artifactPaths", []any{})
			delete(h.stopControllers, key)
			if h.stoppedLaunches[key] {
				if c := h.children[key]; c != nil {
					return c, nil
				}
				failure.set("stopped", true)
				return failure, nil
			}
			h.children[key] = failure
			e := WorkflowScriptTraceEntry{Operation: "run", Key: key, State: "failed", DurationMs: new(time.Since(startedAt).Milliseconds()), GeneratedLaneKey: generatedLaneKey, Error: text}
			e.Phase, e.Label, e.Agent = workflowStringMetadata(params)
			h.trace = append(h.trace, e)
			h.traceChanged()
			h.notifyChildSettled(key, failure)
			return failure, nil
		}
		normalized := result
		if !objBool(result, "ok") && objStr(result, "state") != "running" && !objTruthy(result, "error") {
			normalized = result.clone()
			normalized.set("error", result.vals["output"])
		}
		if len(lineage) > 0 && objStr(normalized, "runId") != "" {
			normalized = normalized.clone()
			cont := newObject()
			ids := dedupeStrings(append(append([]string(nil), lineage...), objStr(normalized, "runId")))
			l := make([]any, len(ids))
			for i, id := range ids {
				l[i] = id
			}
			cont.set("runIds", l)
			normalized.set("continuation", cont)
		}
		delete(h.stopControllers, key)
		if h.stoppedLaunches[key] {
			if c := h.children[key]; c != nil {
				return c, nil
			}
			return normalized, nil
		}
		h.children[key] = normalized
		if isAcceptanceMetadataRecovery(normalized) {
			h.recoveryBarrier = &key
		}
		state := "failed"
		switch {
		case objStr(normalized, "state") == "running":
			state = "started"
		case objBool(normalized, "ok"):
			state = "completed"
		case objTruthy(normalized, "stopped"):
			state = "stopped"
		case objTruthy(normalized, "detached"):
			state = "detached"
		}
		e := WorkflowScriptTraceEntry{Operation: "run", Key: key, State: state, DurationMs: new(time.Since(startedAt).Milliseconds()), GeneratedLaneKey: generatedLaneKey, Agent: objStr(normalized, "agent"), RunID: objStr(normalized, "runId")}
		ph, lb, ag := workflowStringMetadata(params)
		e.Phase, e.Label = ph, lb
		if e.Agent == "" {
			e.Agent = ag
		}
		if !objBool(normalized, "ok") && objStr(normalized, "state") != "running" {
			if t, ok := objHasString(normalized, "error"); ok && normalized.vals["error"] != nil {
				e.Error = t
			} else {
				e.Error = nonNilString(normalized, "output")
			}
		}
		h.trace = append(h.trace, e)
		h.traceChanged()
		h.notifyChildSettled(key, normalized)
		return normalized, nil
	})
	h.launches[key] = &wfLaunch{fingerprint: fingerprint, promise: recorded, observed: callObserved, generatedLaneKey: generatedLaneKey}
	h.launchOrder = append(h.launchOrder, key)
	h.order = append(h.order, key)
	e := WorkflowScriptTraceEntry{Operation: "run", Key: key, State: "started", GeneratedLaneKey: generatedLaneKey}
	e.Phase, e.Label, e.Agent = workflowStringMetadata(params)
	h.trace = append(h.trace, e)
	h.traceChanged()
	h.respondFuture(callID, asAny(h.loop, deliver(recorded)), "runs.run('"+key+"') result", func(err error) { h.children[key] = responseBoundaryFailure(key, err) })
}

func nonNilString(o *jsObject, k string) string {
	switch x := o.vals[k].(type) {
	case string:
		return x
	case nil, undef:
		return ""
	}
	return fmt.Sprint(o.vals[k])
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// combinedAbortContext is combinedAbortSignal: the first of a and b to be cancelled cancels the result with its cause.
func combinedAbortContext(a, b context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(context.Background())
	stopA := context.AfterFunc(a, func() { cancel(context.Cause(a)) })
	stopB := context.AfterFunc(b, func() { cancel(context.Cause(b)) })
	if a.Err() != nil {
		cancel(context.Cause(a))
	} else if b.Err() != nil {
		cancel(context.Cause(b))
	}
	return ctx, func() { stopA(); stopB() }
}

func parseWorkflowResumeReference(v any) (*WorkflowResumeRef, string) {
	o, ok := v.(*jsObject)
	if !ok {
		return nil, ""
	}
	for _, k := range o.order() {
		if k != "workflowRunId" && k != "key" && k != "latest" {
			return nil, "keyed resume contains unsupported fields."
		}
	}
	id, ok := o.vals["workflowRunId"].(string)
	if !ok || jsTrim(id) == "" {
		return nil, "keyed resume workflowRunId must be non-empty."
	}
	key, err := workflowValidateKey(o.vals["key"], "keyed resume")
	if err != nil {
		return nil, err.Error()
	}
	if l, _ := o.vals["latest"].(bool); !l {
		return nil, "keyed resume requires latest: true."
	}
	return &WorkflowResumeRef{WorkflowRunID: jsTrim(id), Key: key}, ""
}

func workflowGateShapeErrorGo(gate any) string {
	shape := "gate must be a non-empty command string or { command, output?: \"json\", schema?, timeoutMs? }."
	if s, ok := gate.(string); ok {
		if jsTrim(s) != "" {
			return ""
		}
		return shape
	}
	o, ok := gate.(*jsObject)
	if !ok {
		return shape
	}
	for _, k := range o.order() {
		if !wfGateKeys[k] {
			return "gate." + k + " is not supported."
		}
	}
	if c, ok := o.vals["command"].(string); !ok || jsTrim(c) == "" {
		return shape
	}
	if out, has := o.vals["output"]; has {
		if _, u := out.(undef); !u && out != "json" {
			return "gate.output must be \"json\" when present."
		}
	}
	if sc, has := o.vals["schema"]; has {
		if _, u := sc.(undef); !u {
			if o.vals["output"] != "json" {
				return "gate.schema requires gate.output: \"json\"."
			}
			if _, isObj := sc.(*jsObject); !isObj {
				return "gate.schema must be a JSON Schema object."
			}
		}
	}
	if to, has := o.vals["timeoutMs"]; has {
		if _, u := to.(undef); !u {
			f, ok := to.(float64)
			if !ok || f != float64(int64(f)) || f < 1 {
				return "gate.timeoutMs must be an integer >= 1."
			}
		}
	}
	return ""
}

func workflowGateAcceptanceConflictGo(gate, acceptance any) string {
	render := func(v any) string {
		e := marshalJSON(v, "")
		if jsLen(e) > 120 {
			e = string(utf16Slice(e, 120)) + "..."
		}
		return e
	}
	return " Both fields were present: gate=" + render(gate) + " acceptance=" + render(acceptance) + "."
}

var workflowAutoResumeParamKeys = []string{"acceptance", "agentContract", "baseRef", "index", "intercomBridge", "label", "lane", "maxRuntimeMs", "output", "outputMode", "outputSchema", "phase", "skill", "skills", "task", "timeoutMs", "toolBudget", "worktree"}

func isZeroUsage(v any) bool {
	u, ok := v.(*jsObject)
	if !ok {
		return false
	}
	zero := func(o *jsObject, k string) bool {
		x, has := o.vals[k]
		if !has || x == nil {
			return true
		}
		f, isNum := x.(float64)
		return isNum && f == 0
	}
	cost, isRec := u.vals["cost"].(*jsObject)
	return zero(u, "input") && zero(u, "output") && zero(u, "cacheRead") && zero(u, "cacheWrite") && (!isRec || zero(cost, "total"))
}

// setupAbortResumeParams is setupAbortResumeParams (scripted-workflow.ts:2054): a child that failed before doing anything, with
// "This operation was aborted", is resumed once.
func setupAbortResumeParams(params, result *jsObject, ctx context.Context) *jsObject {
	runID := objStr(result, "runId")
	if ctx.Err() != nil || objStr(result, "state") == "running" || objBool(result, "ok") || objTruthy(result, "stopped") || objTruthy(result, "interrupted") || runID == "" {
		return nil
	}
	var child *jsObject
	if rs, ok := result.vals["results"].([]any); ok && len(rs) == 1 {
		child, _ = rs[0].(*jsObject)
	}
	errText := objStr(result, "error")
	if child != nil {
		if e, ok := child.vals["error"].(string); ok {
			errText = e
		}
	}
	if errText != "This operation was aborted" || !isZeroUsage(childUsage(child)) {
		return nil
	}
	var messages []any
	if child != nil {
		messages, _ = child.vals["messages"].([]any)
	}
	var message any
	for i := len(messages) - 1; i >= 0; i-- {
		if m, ok := messages[i].(*jsObject); ok && objStr(m, "role") == "assistant" {
			message = m
			break
		}
	}
	if message != nil {
		m := message.(*jsObject)
		if objStr(m, "stopReason") != "error" || objStr(m, "errorMessage") != errText {
			return nil
		}
		content, ok := m.vals["content"].([]any)
		if !ok || len(content) > 0 || !isZeroUsage(m.vals["usage"]) {
			return nil
		}
		if _, has := m.vals["diagnostics"]; has {
			return nil
		}
		if _, has := m.vals["responseId"]; has {
			return nil
		}
	}
	task := "Continue after the setup abort."
	if t, ok := params.vals["task"].(string); ok && jsTrim(t) != "" {
		task = jsTrim(t)
	}
	out := newObject()
	out.set("resume", runID)
	out.set("task", task)
	for _, k := range workflowAutoResumeParamKeys {
		if v, has := params.vals[k]; has {
			out.set(k, v)
		}
	}
	out.set("resume", runID)
	out.set("task", task)
	return out
}

func childUsage(child *jsObject) any {
	if child == nil {
		return nil
	}
	return child.vals["usage"]
}
