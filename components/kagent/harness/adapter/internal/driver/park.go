package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/google/uuid"

	"github.com/MichaelKinsy/pigpen/components/kagent/harness/adapter/internal/config"
	"github.com/MichaelKinsy/pigpen/components/kagent/harness/adapter/internal/journal"
	"github.com/MichaelKinsy/pigpen/components/kagent/harness/adapter/internal/session"
	"github.com/kagent-dev/kagent/go/harness/runtime"
	"github.com/kagent-dev/kagent/go/pkg/logging"
)

// confirmPayload is the JSON in the message of the policy extension's confirm
// request (design section 4).
type confirmPayload struct {
	V          int             `json:"v"`
	Kind       string          `json:"kind"`
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Input      json.RawMessage `json:"input"`
}

// handleUI deals with an extension_ui_request. handled is true when the turn
// ended (parked); otherwise the loop continues.
func (d *Driver) handleUI(ctx context.Context, p *proc, sp spec, tr *translator, raw []byte) (outcome runtime.Outcome, handled bool, err error) {
	var rec uiRequestRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return runtime.Outcome{}, false, fmt.Errorf("decode extension_ui_request: %w", err)
	}
	log := logging.FromContext(ctx)
	switch rec.Method {
	case "notify":
		if rec.NotifyType == "error" {
			log.WarnContext(ctx, "PiG extension reported an error", "message", rec.Message)
		}
		return runtime.Outcome{}, false, nil
	case "setStatus", "setWidget", "setTitle", "set_editor_text":
		return runtime.Outcome{}, false, nil
	case "confirm":
		var payload confirmPayload
		if json.Unmarshal([]byte(rec.Message), &payload) == nil && payload.V == 1 && (payload.Kind == string(journal.KindApproval) || payload.Kind == string(journal.KindAskUser)) {
			outcome, err := d.park(ctx, p, sp, tr, payload)
			return outcome, true, err
		}
	}
	// A dialog the adapter cannot answer must not hang the turn.
	log.WarnContext(ctx, "cancelling an extension dialog the harness cannot answer", "method", rec.Method)
	respondCancelled(p, rec.ID)
	return runtime.Outcome{}, false, nil
}

// buildRequest turns the confirm payload into the public request and the record fields.
func (d *Driver) buildRequest(payload confirmPayload) (rec journal.Record, err error) {
	if payload.ToolCallID == "" || payload.ToolName == "" {
		return rec, errors.New("the confirm request has no tool call")
	}
	rec.CallID = payload.ToolCallID
	var input map[string]any
	if err := json.Unmarshal(payload.Input, &input); err != nil || input == nil {
		return rec, errors.New("the confirm request's input is not a JSON object")
	}
	args := input
	switch journal.Kind(payload.Kind) {
	case journal.KindApproval:
		server, tool, ok := SplitMCPName(d.cfg.Compiled.Policy.MCPServers, payload.ToolName)
		if !ok {
			return rec, fmt.Errorf("approval for %q does not name a configured MCP server", payload.ToolName)
		}
		rec.Kind, rec.Server, rec.Tool = journal.KindApproval, server, tool
		rec.Approval = &runtime.ApprovalRequest{
			ID: uuid.NewString(), CallID: payload.ToolCallID, Name: payload.ToolName, Args: args,
			Hint: "PiG wants to call " + payload.ToolName + " with " + argumentSummary(payload.Input) + ". Approve or reject this call.",
		}
	case journal.KindAskUser:
		if payload.ToolName != config.AskUserTool {
			return rec, fmt.Errorf("ask_user request names tool %q", payload.ToolName)
		}
		questions, err := parseQuestions(payload.Input)
		if err != nil {
			return rec, err
		}
		rec.Kind = journal.KindAskUser
		rec.Question = &runtime.AskUserRequest{ID: uuid.NewString(), Questions: questions, Hint: "PiG needs more information before continuing."}
	default:
		return rec, fmt.Errorf("unknown confirm kind %q", payload.Kind)
	}
	return rec, nil
}

func parseQuestions(input json.RawMessage) ([]runtime.AskUserQuestion, error) {
	var parsed struct {
		Questions []struct {
			Question string   `json:"question"`
			Choices  []string `json:"choices"`
			Multiple bool     `json:"multiple"`
		} `json:"questions"`
	}
	if err := json.Unmarshal(input, &parsed); err != nil || len(parsed.Questions) == 0 {
		return nil, errors.New("ask_user input has no questions")
	}
	questions := make([]runtime.AskUserQuestion, 0, len(parsed.Questions))
	for i, q := range parsed.Questions {
		if q.Question == "" {
			return nil, errors.New("ask_user input has an empty question")
		}
		questions = append(questions, runtime.AskUserQuestion{ID: fmt.Sprintf("q%d", i+1), Question: q.Question, Choices: q.Choices, Multiple: q.Multiple})
	}
	return questions, nil
}

// park ends the turn at an approval or question. Order matters: the public
// call is announced, pig's stdin is closed and the process reaped (nothing in
// the batch has run), and only then is the record written, so a snapshot taken
// after input-required always sees a quiescent session and a complete record.
func (d *Driver) park(ctx context.Context, p *proc, sp spec, tr *translator, payload confirmPayload) (runtime.Outcome, error) {
	rec, err := d.buildRequest(payload)
	if err != nil {
		return runtime.Outcome{}, err
	}
	if err := tr.toolCall(payload.ToolCallID, payload.ToolName, payload.Input); err != nil {
		return runtime.Outcome{}, err
	}
	if err := ctx.Err(); err != nil {
		return runtime.Outcome{}, err // cancelled while pausing: do not park
	}
	if err := p.stop(); err != nil {
		return runtime.Outcome{}, &runtime.CancellationError{Err: err}
	}
	if err := ctx.Err(); err != nil {
		return runtime.Outcome{}, err
	}
	path, found, err := session.Find(d.cfg.SessionDir, sp.sessionID)
	if err != nil || !found {
		return runtime.Outcome{}, errors.Join(errors.New("the PiG session file is missing after the park"), err)
	}
	file, err := session.Scan(path)
	if err != nil {
		return runtime.Outcome{}, err
	}
	tail, err := file.ParkedTail()
	if err != nil {
		return runtime.Outcome{}, err
	}
	if err := d.checkAgainstSession(ctx, tail, payload); err != nil {
		return runtime.Outcome{}, err
	}
	rec.Version, rec.Phase = journal.Version, journal.Waiting
	rec.TaskID, rec.ContextID, rec.Origin, rec.SessionID = sp.taskID, sp.contextID, sp.origin, sp.sessionID
	rec.AssistantEntryID, rec.Calls = tail.ID, tail.Calls
	if err := d.journal.Save(&rec); err != nil {
		return runtime.Outcome{}, err
	}
	return runtime.Outcome{Pending: &pending{d: d, record: rec}}, nil
}

// checkAgainstSession ties the confirm request to the assistant message the session ends at: the call must be one of
// its calls, under the same tool name. The extension's input is what the human approves and what the adapter runs;
// if it differs from the model's own arguments that is logged, because only a faulty extension or hook changes them.
func (d *Driver) checkAgainstSession(ctx context.Context, tail *session.Entry, payload confirmPayload) error {
	for _, call := range tail.Calls {
		if call.ID != payload.ToolCallID {
			continue
		}
		if call.Name != payload.ToolName {
			return fmt.Errorf("the confirm request names tool %q but the session's call %s is %q", payload.ToolName, call.ID, call.Name)
		}
		var recorded, approved any
		if json.Unmarshal(call.Arguments, &recorded) != nil || json.Unmarshal(payload.Input, &approved) != nil || !reflect.DeepEqual(recorded, approved) {
			logging.FromContext(ctx).WarnContext(ctx, "the approved arguments differ from the model's recorded call", "call", call.ID, "tool", call.Name)
		}
		return nil
	}
	return fmt.Errorf("the confirm request names call %q, which the session's assistant message does not contain", payload.ToolCallID)
}

// argumentSummary is a bounded one-line view of the call's arguments for the approver.
func argumentSummary(input json.RawMessage) string {
	const limit = 400
	text := string(input)
	if len(text) > limit {
		text = text[:limit] + "...(truncated)"
	}
	return text
}
