package driver

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/MichaelKinsy/pigpen/components/kagent/harness/adapter/internal/config"
	"github.com/MichaelKinsy/pigpen/components/kagent/harness/adapter/internal/journal"
	"github.com/MichaelKinsy/pigpen/components/kagent/harness/adapter/internal/session"
	"github.com/kagent-dev/kagent/go/harness/runtime"
	"github.com/kagent-dev/kagent/go/pkg/logging"
)

// RejectionText is what the model sees for a rejected call: kagent's ADK wording, which appends a space and the
// trimmed reason only when that reason is not blank.
func RejectionText(reason string) string {
	text := "Tool call was rejected by user."
	if reason = strings.TrimSpace(reason); reason != "" {
		text += " Reason: " + reason
	}
	return text
}

// validationError marks a Resume the adapter refuses because the response itself is wrong (not the turn): it
// leaves the record as it was, so a corrected response can still be applied.
type validationError struct{ err error }

func (e *validationError) Error() string { return e.err.Error() }
func (e *validationError) Unwrap() error { return e.err }

// AnswerText formats ask_user answers as the tool result of that call.
func AnswerText(questions []runtime.AskUserQuestion, answers [][]string) string {
	var b strings.Builder
	for i, q := range questions {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("Question: " + q.Question + "\nAnswer: " + strings.Join(answers[i], ", "))
	}
	return b.String()
}

// pending is the durable handle for a parked turn. It holds only a copy of the
// record taken when it was created; every operation re-reads the file, because
// Substrate can swap /data under a constructed process.
type pending struct {
	d      *Driver
	record journal.Record
	// gatedClosed is set once the A2A tool artifact of the gated call has its result (the continuation's, or the
	// close that Resume emitted when it was cancelled or abandoned). Only this handle's own emissions are tracked: a handle built by
	// RestorePending or repark starts open, as the task's artifact does.
	gatedClosed bool
}

var _ runtime.PendingTurn = (*pending)(nil)

func (p *pending) Request() runtime.InputRequest { return p.record.Request() }

// TraceOrigin lets the executor link the resumed segment to the one that parked.
func (p *pending) TraceOrigin() runtime.TraceOrigin { return p.record.Origin }

// RestorePending is a local read of the record, on every call.
func (d *Driver) RestorePending() (string, string, runtime.PendingTurn, error) {
	r, err := d.journal.Load()
	if errors.Is(err, journal.ErrCorrupt) {
		// A record that cannot be read back is not replayed. It is moved aside and reported once as an abandoned
		// session, so the Actor takes new work after this one failed task instead of refusing every task forever.
		if qErr := d.journal.Quarantine(); qErr != nil {
			return "", "", nil, errors.Join(err, qErr)
		}
		return "", "", nil, errors.Join(runtime.ErrSessionAbandoned, err)
	}
	if err != nil || r == nil {
		return "", "", nil, err
	}
	switch r.Phase {
	case journal.Complete, journal.Canceled:
		return "", "", nil, nil
	case journal.Abandoned, journal.Executing, journal.Continuing:
		// An approved call may have run, or a model turn may have started.
		return "", "", nil, runtime.ErrSessionAbandoned
	}
	return r.TaskID, r.ContextID, &pending{d: d, record: *r}, nil
}

func (p *pending) current() (*journal.Record, error) {
	r, err := p.d.journal.Load()
	if err != nil {
		return nil, err
	}
	if r == nil || r.TaskID != p.record.TaskID || r.ContextID != p.record.ContextID || r.SessionID != p.record.SessionID ||
		r.AssistantEntryID != p.record.AssistantEntryID || !reflect.DeepEqual(r.Request(), p.record.Request()) {
		return nil, errors.New("the PiG pending record no longer matches")
	}
	return r, nil
}

func validateResponse(request runtime.InputRequest, response runtime.InputResponse) error {
	switch request := request.(type) {
	case *runtime.ApprovalRequest:
		decision, ok := response.(*runtime.ApprovalDecision)
		if !ok {
			return errors.New("PiG is waiting for a structured tool approval response")
		}
		if decision.ID != request.ID {
			return fmt.Errorf("tool approval response ID %q does not match pending ID %q", decision.ID, request.ID)
		}
	case *runtime.AskUserRequest:
		answer, ok := response.(*runtime.AskUserResponse)
		if !ok {
			return errors.New("PiG is waiting for a structured ask-user response")
		}
		if answer.ID != request.ID {
			return fmt.Errorf("ask-user response ID %q does not match pending ID %q", answer.ID, request.ID)
		}
		if len(answer.Answers) != len(request.Questions) {
			return errors.New("ask-user response must answer every pending question")
		}
	default:
		return fmt.Errorf("unsupported pending input request %T", request)
	}
	return nil
}

// Resume carries the human's answer into the session and continues the turn.
// Phases: waiting, executing, result, continuing, complete (canceled and
// abandoned are terminal). A crash in executing or continuing is never
// replayed: RestorePending reports runtime.ErrSessionAbandoned.
func (p *pending) Resume(ctx context.Context, response runtime.InputResponse, sink runtime.EventSink) (outcome runtime.Outcome, returnErr error) {
	defer func() {
		if ctx.Err() == nil || (returnErr == nil && outcome.Pending == nil) {
			return // not cancelled, or the turn had already finished
		}
		var unsafe *runtime.CancellationError
		if errors.As(returnErr, &unsafe) {
			// pig could not be confirmed gone: the continuation may still be writing, so this is not a clean
			// cancellation and the record must not read as one.
			outcome = runtime.Outcome{}
			p.closeGated(sink, unfinishedResult())
			if err := p.markAbandoned(); err != nil {
				returnErr = errors.Join(returnErr, err)
			}
			return
		}
		// Cancelled mid-resume: tombstone the record (or the one a nested park
		// replaced it with) instead of replaying.
		target := outcome.Pending
		if target == nil {
			target = p
		}
		// Cancel itself never emits (see below); this is the live Resume sink, so the gated call's artifact is closed here.
		if tp, ok := target.(*pending); ok {
			tp.closeGatedAtCancel(sink)
		}
		if err := target.Cancel(context.WithoutCancel(ctx), sink); err != nil {
			outcome = runtime.Outcome{}
			if errors.Is(err, runtime.ErrSessionAbandoned) {
				// The approved call may have run: say so, never present it as a clean cancellation.
				returnErr = errors.Join(context.Canceled, err)
				return
			}
			returnErr = &runtime.CancellationError{Err: errors.Join(returnErr, err)}
			return
		}
		outcome, returnErr = runtime.Outcome{}, context.Canceled
	}()
	defer func() {
		var invalid *validationError
		if returnErr != nil && ctx.Err() == nil && !errors.As(returnErr, &invalid) {
			p.closeGated(sink, unfinishedResult())
			returnErr = p.abandon(returnErr)
		}
	}()
	r, err := p.current()
	if err != nil {
		return runtime.Outcome{}, err
	}
	if err := validateResponse(r.Request(), response); err != nil {
		return runtime.Outcome{}, &validationError{err}
	}
	switch r.Phase {
	case journal.Executing, journal.Continuing:
		return runtime.Outcome{}, errors.New("the PiG continuation outcome is uncertain; automatic replay is disabled")
	case journal.Waiting, journal.Result:
	default:
		return runtime.Outcome{}, errors.New("the PiG continuation is already resolved")
	}
	if r.Phase == journal.Result {
		if decision, ok := response.(*runtime.ApprovalDecision); ok && !reflect.DeepEqual(r.Decision, decision) {
			return runtime.Outcome{}, &validationError{errors.New("the approval decision changed after execution")}
		}
		if answer, ok := response.(*runtime.AskUserResponse); ok && !reflect.DeepEqual(r.Answer, answer) {
			return runtime.Outcome{}, &validationError{errors.New("the answer changed after resolution")}
		}
	} else {
		reparked, outcome, err := p.resolve(ctx, r, response, sink)
		if reparked || err != nil {
			return outcome, err
		}
	}
	return p.continueTurn(ctx, r, sink)
}

// resolve moves waiting to result: it runs the approved call, builds the
// rejection text, or formats the answers. reparked means the request stays
// waiting because the call provably did not run.
func (p *pending) resolve(ctx context.Context, r *journal.Record, response runtime.InputResponse, sink runtime.EventSink) (reparked bool, outcome runtime.Outcome, err error) {
	switch response := response.(type) {
	case *runtime.ApprovalDecision:
		r.Decision = response
		if !response.Approved {
			out := session.TextOutcome(RejectionText(response.RejectionReason), true)
			r.Result = &out
			break
		}
		server, ok := p.d.server(r.Server)
		if !ok {
			outcome, err := p.repark(ctx, r, errors.New("the approved call's MCP server is no longer configured"))
			return true, outcome, err
		}
		r.Phase = journal.Executing
		if err := p.d.journal.Save(r); err != nil {
			return false, runtime.Outcome{}, err
		}
		p.d.point("executing")
		call := runtime.ToolCall{ID: r.CallID, Name: r.Approval.Name, Arguments: r.Approval.Args, MCPServer: r.Server, MCPTool: RawToolName(p.d.cfg.Compiled.Policy.MCPServers, r.Server, r.Approval.Name)}
		observer, observed := sink.(runtime.ToolExecutionSink)
		if observed {
			observer.ToolExecutionStarted(call)
		}
		result, callErr := p.d.cfg.Tools.Call(ctx, server, r.Approval.Name, r.Approval.Args)
		p.d.point("called")
		var notExecuted *notExecutedError
		didNotRun := errors.As(callErr, &notExecuted)
		if observed {
			status := runtime.ToolRan
			if didNotRun {
				status = runtime.ToolDeclined
			}
			observer.ToolExecutionFinished(runtime.ToolResult{ID: r.CallID, Name: r.Approval.Name, Result: result, IsError: callErr != nil || result != nil && result.IsError, Status: status})
		}
		if callErr != nil {
			if didNotRun {
				outcome, err := p.repark(ctx, r, callErr)
				return true, outcome, err
			}
			return false, runtime.Outcome{}, fmt.Errorf("execute approved MCP call (outcome may be uncertain): %w", callErr)
		}
		out, dropped := OutcomeFromMCP(result)
		if dropped > 0 {
			logging.FromContext(ctx).WarnContext(ctx, "approved MCP result truncated before injection", "dropped_bytes", dropped, "kept_text_limit", MaxResultTextBytes)
		}
		r.Result = &out
	case *runtime.AskUserResponse:
		r.Answer = response
		out := session.TextOutcome(AnswerText(r.Question.Questions, response.Answers), false)
		r.Result = &out
	}
	r.Phase = journal.Result
	if err := p.d.journal.Save(r); err != nil {
		return false, runtime.Outcome{}, err
	}
	p.d.point("result")
	return false, runtime.Outcome{}, nil
}

// continueTurn appends the tool results, then runs a fresh pig with one short
// continuation prompt and streams its events.
func (p *pending) continueTurn(ctx context.Context, r *journal.Record, sink runtime.EventSink) (runtime.Outcome, error) {
	path, found, err := session.Find(p.d.cfg.SessionDir, r.SessionID)
	if err != nil || !found {
		return runtime.Outcome{}, errors.Join(errors.New("the parked PiG session file is missing"), err)
	}
	results := injectedResults(r)
	if _, err := session.Inject(path, r.AssistantEntryID, results, p.d.cfg.Now()); err != nil {
		return runtime.Outcome{}, err
	}
	p.d.point("injected")
	r.Phase = journal.Continuing
	if err := p.d.journal.Save(r); err != nil {
		return runtime.Outcome{}, err
	}
	p.d.point("continuing")
	name := callName(r)
	sp := spec{
		taskID: r.TaskID, contextID: r.ContextID, origin: r.Origin, sessionID: r.SessionID, prompt: ContinuationPrompt,
		afterAccept: func() error {
			p.gatedClosed = true
			return sink.ToolResult(runtime.ToolResult{ID: r.CallID, Name: name, Result: r.Result.Content, IsError: r.Result.IsError, Status: declined(r)})
		},
	}
	outcome, err := p.d.run(ctx, sp, sink)
	var notStarted *notStartedError
	if errors.As(err, &notStarted) {
		return p.repark(ctx, r, err)
	}
	if err != nil || outcome.Pending != nil {
		return outcome, err // a nested park has already replaced the record
	}
	// A failed prompt (PiG refused it) ends the turn without afterAccept having run; the result is in the session
	// all the same, so the gated call's artifact gets it here.
	p.closeGated(sink, runtime.ToolResult{Result: r.Result.Content, IsError: r.Result.IsError, Status: declined(r)})
	r.Phase = journal.Complete
	if err := p.d.journal.Save(r); err != nil {
		return runtime.Outcome{}, err
	}
	return outcome, nil
}

func declined(r *journal.Record) runtime.ToolStatus {
	if r.Kind == journal.KindApproval && r.Decision != nil && !r.Decision.Approved {
		return runtime.ToolDeclined
	}
	return runtime.ToolRan
}

func (d *Driver) server(name string) (s config.PolicyServer, ok bool) {
	for _, candidate := range d.cfg.Compiled.Policy.MCPServers {
		if candidate.Name == name {
			return candidate, true
		}
	}
	return config.PolicyServer{}, false
}

// repark keeps the same public request after a proven non-execution: the
// record returns to waiting (or result, when the continuation prompt never
// reached pig) and a fresh handle is returned for the executor to park again.
func (p *pending) repark(ctx context.Context, r *journal.Record, cause error) (runtime.Outcome, error) {
	if r.Phase == journal.Continuing {
		r.Phase = journal.Result
	} else {
		r.Phase, r.Decision, r.Answer, r.Result = journal.Waiting, nil, nil, nil
	}
	if err := p.d.journal.Save(r); err != nil {
		return runtime.Outcome{}, errors.Join(cause, err)
	}
	logging.FromContext(ctx).ErrorContext(ctx, "PiG continuation remains pending after proven non-execution", "phase", r.Phase, "error", cause)
	return runtime.Outcome{Pending: &pending{d: p.d, record: *r}}, nil
}

// markAbandoned records that the outcome can no longer be known.
func (p *pending) markAbandoned() error {
	r, err := p.current()
	if err != nil {
		return err
	}
	if r.Phase == journal.Complete || r.Phase == journal.Canceled || r.Phase == journal.Abandoned {
		return nil
	}
	r.Phase = journal.Abandoned
	return p.d.journal.Save(r)
}

// abandon prevents a failed task from being restored as pending input.
func (p *pending) abandon(cause error) error {
	r, err := p.current()
	if err != nil {
		return errors.Join(cause, err)
	}
	if r.Phase == journal.Complete || r.Phase == journal.Canceled {
		return cause
	}
	r.Phase = journal.Abandoned
	if err := p.d.journal.Save(r); err != nil {
		return errors.Join(cause, err)
	}
	return errors.Join(runtime.ErrSessionAbandoned, cause)
}

// closeGated gives the gated call's tool artifact a result when this handle has not already. It takes the live
// sink of a Run or Resume only; PendingTurn.Cancel must never call it (see Cancel).
func (p *pending) closeGated(sink runtime.EventSink, res runtime.ToolResult) {
	if p.gatedClosed {
		return
	}
	p.gatedClosed = true
	res.ID, res.Name = p.record.CallID, callName(&p.record)
	_ = sink.ToolResult(res)
}

// closeGatedAtCancel closes the gated call when Resume is cancelled: with its recorded result when the call
// already ran, otherwise as unfinished.
func (p *pending) closeGatedAtCancel(sink runtime.EventSink) {
	res := unfinishedResult()
	if r, err := p.current(); err == nil && r.Phase == journal.Result && r.Result != nil {
		res = runtime.ToolResult{Result: r.Result.Content, IsError: r.Result.IsError, Status: declined(r)}
	}
	p.closeGated(sink, res)
}

// Cancel tombstones the record. No process is alive while parked, so there is
// no native process to reap and no usage to emit. It emits nothing at all, not even
// through a sink that happens to be an EventSink: kagent cancels a parked task with a
// bare sink that has no request context, and a tool result sent to it panics. A task
// cancelled while parked therefore relies on the executor's own task state; the gated
// call's artifact is closed by Run and Resume only, which hold the live sink.
func (p *pending) Cancel(_ context.Context, _ runtime.ModelCallSink) error {
	r, err := p.current()
	if err != nil {
		return err
	}
	switch r.Phase {
	case journal.Abandoned:
		return runtime.ErrSessionAbandoned
	case journal.Executing:
		// The approved call was sent (or may have been) and no result was recorded: cancelling cannot prove it did
		// not run, and a tombstone would let the next turn re-issue it. Same rule as a crash in this phase.
		r.Phase = journal.Abandoned
		if err := p.d.journal.Save(r); err != nil {
			return err
		}
		return runtime.ErrSessionAbandoned
	case journal.Complete:
		return nil
	case journal.Result:
		// The approved call ran and its result is recorded. A tombstone alone would leave the session showing a
		// dangling call, which PiG masks as "No result provided", and the model could repeat a side effect that
		// already happened. Put the result into the session first.
		path, found, err := session.Find(p.d.cfg.SessionDir, r.SessionID)
		if err == nil && found {
			_, err = session.Inject(path, r.AssistantEntryID, injectedResults(r), p.d.cfg.Now())
		} else if err == nil {
			err = errors.New("the parked PiG session file is missing")
		}
		if err != nil {
			r.Phase = journal.Abandoned
			return errors.Join(runtime.ErrSessionAbandoned, err, p.d.journal.Save(r))
		}
	}
	r.Phase = journal.Canceled
	return p.d.journal.Save(r)
}

// injectedResults has one entry per call of the interrupted assistant message:
// the resolved call gets its result, every sibling is reported as not executed
// (the batch's hooks all ran before any call, so none of them executed).
//
// Caveat: that invariant holds for a parallel batch only. PiG v0.7.0 runs a batch sequentially, one call at a time,
// if any tool declares execution_mode sequential (agent/tool_execution.go), and then a sibling ahead of the gated
// call may have executed before the park. No tool in the shipped image declares it (only extension tools can), so
// the adapter does not handle it; a sequential tool would need the siblings' recorded results instead.
func injectedResults(r *journal.Record) []session.Result {
	results := make([]session.Result, 0, len(r.Calls))
	for _, call := range r.Calls {
		out := session.TextOutcome(session.NotExecutedText(r.Kind), true)
		if call.ID == r.CallID {
			out = *r.Result
		}
		results = append(results, session.Result{CallID: call.ID, Name: call.Name, Out: out})
	}
	return results
}

func callName(r *journal.Record) string {
	for _, call := range r.Calls {
		if call.ID == r.CallID {
			return call.Name
		}
	}
	return ""
}
