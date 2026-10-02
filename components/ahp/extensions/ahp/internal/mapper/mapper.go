package mapper

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"
)

// Event is one pi agent event, decoded JSON.
type Event = map[string]any

// Outcome is how a turn ended.
type Outcome string

const (
	// OutcomeDefault lets the mapper decide from what it saw.
	OutcomeDefault   Outcome = ""
	OutcomeComplete  Outcome = "complete"
	OutcomeCancelled Outcome = "cancelled"
	OutcomeError     Outcome = "error"
)

// Options configure a TurnMapper.
type Options struct {
	// WorkingDirectory renders tool paths relative to it, so activity reads as `Reading src/x.ts`.
	WorkingDirectory string
	// Now is the clock (defaults to time.Now); tests pin it.
	Now func() time.Time
}

type openPart struct {
	partID  string
	kind    ahptypes.ResponsePartKind
	created bool
}

// TurnMapper maps one AHP turn (port of the TurnMapper class of src/pi/event-mapper.ts).
//
// Three structural mismatches it bridges:
//
//  1. Turn granularity. pi nests an agent run (agent_start..agent_end), a pi turn (one assistant
//     message) and content blocks; AHP has a turn (one user message through to completion) and
//     its response parts. One AHP turn therefore spans everything from prompt() to agent_settled,
//     which may hold several agent runs (auto-retry, post-compaction retry). Mapping agent_end to
//     chat/turnComplete would close the turn early and the reducer would drop every later action.
//  2. Part identity. contentIndex restarts at 0 for every assistant message and the stream has no
//     message id, so parts are keyed `<turnId>:<message ordinal>:<contentIndex>`.
//  3. Part creation. chat/delta appends to a part that must already exist (an unknown partId is a
//     silent no-op), so parts are created lazily, on the first content that lands in them.
//
// A mapper is created when a turn starts and discarded when it ends. It is not safe for
// concurrent use; the caller serialises Handle and Finish.
type TurnMapper struct {
	turnID        string // the turn being mapped; changes when a message is injected mid-run
	rootTurnID    string
	turnStartedAt int64 // ms since the epoch
	injectedCount int

	messageOrdinal int
	parts          map[int]*openPart
	toolCallsByIdx map[int]string
	liveToolCalls  map[string]string // toolCallId -> how the call reads once it has finished
	outcome        Outcome
	errorMessage   *string
	finished       bool

	opts     Options
	activity *string
}

// NewTurnMapper creates a mapper for a turn that started at startedAtMs (ms since the epoch).
func NewTurnMapper(turnID string, startedAtMs int64, opts ...Options) *TurnMapper {
	m := &TurnMapper{
		turnID: turnID, rootTurnID: turnID, turnStartedAt: startedAtMs,
		messageOrdinal: -1, parts: map[int]*openPart{}, toolCallsByIdx: map[int]string{},
		liveToolCalls: map[string]string{}, outcome: OutcomeComplete,
	}
	if len(opts) > 0 {
		m.opts = opts[0]
	}
	if m.opts.Now == nil {
		m.opts.Now = time.Now
	}
	return m
}

// TurnID is the turn currently being mapped.
func (m *TurnMapper) TurnID() string { return m.turnID }

// Finished reports whether the turn has been closed.
func (m *TurnMapper) Finished() bool { return m.finished }

func (m *TurnMapper) nowMs() int64 { return m.opts.Now().UnixMilli() }

// Handle translates one pi event into zero or more chat actions.
func (m *TurnMapper) Handle(event Event) []ahptypes.StateAction {
	if m.finished {
		return nil
	}
	typ, _ := event["type"].(string)
	switch typ {
	case "message_start":
		return m.onMessageStart(event)
	case "message_update":
		return m.onDelta(asMap(event["assistantMessageEvent"]))
	case "message_end":
		return m.onMessageEnd(event)
	case "tool_execution_start":
		// The tool is about to run: say which one, and with what.
		name, _ := event["toolName"].(string)
		return m.setActivity(strPtr(DescribeToolCall(name, event["args"], m.opts.WorkingDirectory)))
	case "tool_execution_update":
		return m.onToolUpdate(event)
	case "tool_execution_end":
		out := m.onToolEnd(event)
		// Back to the model. Kept out of onToolEnd, which bails early for a call it never saw
		// start: activity is display state and should not depend on that bookkeeping.
		return append(out, m.setActivity(strPtr(ThinkingActivity))...)
	case "agent_settled":
		return m.Finish(OutcomeDefault, "")
	case "agent_start":
		// A run beginning, or resuming after a retry.
		return m.setActivity(strPtr(ThinkingActivity))
	default:
		// agent_end / turn_start / turn_end carry no protocol-visible state of their own: a retry
		// re-enters agent_start inside the same AHP turn, and pi's turn boundaries are just
		// groupings of response parts.
		return nil
	}
}

func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }

// ── Assistant messages ──────────────────────────────────────────────────

func (m *TurnMapper) onMessageStart(event Event) []ahptypes.StateAction {
	message := asMap(event["message"])
	switch message["role"] {
	case "user":
		return m.onInjectedMessage(message)
	case "assistant":
		// A new assistant message restarts contentIndex at 0, so the part map must not leak.
		m.messageOrdinal++
		m.parts = map[int]*openPart{}
		m.toolCallsByIdx = map[int]string{}
	}
	return nil
}

// setActivity emits an activity change, skipping no-ops: a turn fires many events that map to
// the same description, and re-sending it would burn a serverSeq per token.
func (m *TurnMapper) setActivity(activity *string) []ahptypes.StateAction {
	if (m.activity == nil && activity == nil) || (m.activity != nil && activity != nil && *m.activity == *activity) {
		return nil
	}
	m.activity = activity
	return []ahptypes.StateAction{ahptypes.StateAction{Value: &ahptypes.ChatActivityChangedAction{Type: ahptypes.ActionTypeChatActivityChanged, Activity: activity}}}
}

func (m *TurnMapper) onMessageEnd(event Event) []ahptypes.StateAction {
	message := asMap(event["message"])
	if message["role"] != "assistant" {
		return nil
	}
	// A run cancelled or failed before any content streamed produces no `error` delta: the outcome
	// shows up only as the finished message's stopReason. Reading it here keeps such a turn from
	// being reported as a normal completion.
	switch message["stopReason"] {
	case "aborted":
		m.outcome = OutcomeCancelled
		m.errorMessageIfUnset(message["errorMessage"])
	case "error":
		m.outcome = OutcomeError
		m.errorMessageIfUnset(message["errorMessage"])
	}
	model, _ := message["model"].(string)
	if usage := toUsageInfo(asMap(message["usage"]), model); usage != nil {
		return []ahptypes.StateAction{ahptypes.StateAction{Value: &ahptypes.ChatUsageAction{Type: ahptypes.ActionTypeChatUsage, TurnId: m.turnID, Usage: *usage}}}
	}
	return nil
}

// errorMessageIfUnset is `this.#errorMessage ??= value`.
func (m *TurnMapper) errorMessageIfUnset(v any) {
	if m.errorMessage == nil {
		if s, ok := v.(string); ok {
			m.errorMessage = &s
		}
	}
}

func toUsageInfo(usage map[string]any, model string) *ahptypes.UsageInfo {
	if usage == nil {
		return nil
	}
	// UsageInfo only has slots for input/output/cacheRead, but pi reports more (cache writes, a
	// reasoning-token breakdown, computed cost). Dropping them would lose the numbers a client
	// needs to show what a turn cost, so the remainder rides in `_meta`, the protocol's place for
	// provider-specific extras.
	extra := map[string]json.RawMessage{}
	put := func(key string, v any) {
		if raw, err := json.Marshal(v); err == nil {
			extra[key] = raw
		}
	}
	if v, ok := usage["cacheWrite"]; ok && v != nil {
		put("cacheWriteTokens", v)
	}
	if v, ok := usage["reasoning"]; ok && v != nil {
		put("reasoningTokens", v)
	}
	if v, ok := usage["totalTokens"]; ok && v != nil {
		put("totalTokens", v)
	}
	if v, ok := usage["cost"]; ok && v != nil {
		put("cost", v)
	}
	info := &ahptypes.UsageInfo{}
	if f, ok := num(usage["input"]); ok {
		info.InputTokens = i64Ptr(int64(f))
	}
	if f, ok := num(usage["output"]); ok {
		info.OutputTokens = i64Ptr(int64(f))
	}
	if f, ok := num(usage["cacheRead"]); ok {
		info.CacheReadTokens = i64Ptr(int64(f))
	}
	if model != "" {
		info.Model = &model
	}
	if len(extra) > 0 {
		info.Meta = extra
	}
	return info
}

// onInjectedMessage handles a user message pi injects part-way through a run.
//
// pi delivers a steering message as an ordinary user message inside the running turn and stores
// it in the session file as an ordinary user message too, with nothing to mark it as steering. So
// when the conversation is rebuilt from disk it necessarily becomes its own turn, because a user
// message is what starts one. The live path does the same, or the conversation would render one
// way while running and another after a reload: the turn in flight is closed and a fresh one
// opened around the injected message.
//
// The first user message of a run is the prompt itself, which the client already opened the turn
// with, so only later ones split.
func (m *TurnMapper) onInjectedMessage(message map[string]any) []ahptypes.StateAction {
	if m.messageOrdinal < 0 {
		return nil // nothing has streamed yet: this is the run's own prompt
	}
	injected := UserMessageFromPiContent(message["content"])
	completed := m.closeCurrentTurn()

	m.injectedCount++
	m.turnID = m.rootTurnID + "#" + strconv.Itoa(m.injectedCount)
	m.turnStartedAt = m.nowMs()
	m.messageOrdinal = -1
	m.parts = map[int]*openPart{}
	m.toolCallsByIdx = map[int]string{}

	return append(completed, ahptypes.StateAction{Value: &ahptypes.ChatTurnStartedAction{
		Type: ahptypes.ActionTypeChatTurnStarted, TurnId: m.turnID,
		StartedAt: isoTime(m.turnStartedAt), Message: injected,
	}})
}

func (m *TurnMapper) closeCurrentTurn() []ahptypes.StateAction {
	return []ahptypes.StateAction{ahptypes.StateAction{Value: &ahptypes.ChatTurnCompleteAction{
		Type: ahptypes.ActionTypeChatTurnComplete, TurnId: m.turnID, Duration: m.duration(),
	}}}
}

func (m *TurnMapper) duration() int64 {
	if d := m.nowMs() - m.turnStartedAt; d > 0 {
		return d
	}
	return 0
}

// isoTime is Date.toISOString.
func isoTime(ms int64) string { return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z") }

// ── Streaming content ───────────────────────────────────────────────────

func (m *TurnMapper) onDelta(delta map[string]any) []ahptypes.StateAction {
	typ, _ := delta["type"].(string)
	switch typ {
	case "text_delta":
		return m.appendText(delta, ahptypes.ResponsePartKindMarkdown, false)
	case "thinking_delta":
		return m.appendText(delta, ahptypes.ResponsePartKindReasoning, false)
	case "text_end":
		return m.closeBlock(delta, ahptypes.ResponsePartKindMarkdown)
	case "thinking_end":
		return m.closeBlock(delta, ahptypes.ResponsePartKindReasoning)
	case "toolcall_start":
		return m.onToolCallStart(delta)
	case "toolcall_delta":
		return m.onToolCallDelta(delta)
	case "toolcall_end":
		return m.onToolCallReady(delta)
	case "error":
		// Recorded, not emitted: a mid-stream error may still be retried inside the same AHP turn,
		// so only agent_settled decides the turn's outcome.
		if dig(delta, "error", "stopReason") == "aborted" {
			m.outcome = OutcomeCancelled
		} else {
			m.outcome = OutcomeError
		}
		m.errorMessage = nil
		if s, ok := digStr(delta, "error", "errorMessage"); ok {
			m.errorMessage = &s
		}
		return nil
	default:
		// `*_start` / `start` / `done` need no action: parts are created lazily by the first delta.
		return nil
	}
}

// closeBlock handles the end of a text or reasoning block. Normally a no-op, because the deltas
// already built the part. A provider that does not stream a block incrementally emits only
// `*_end` with the finished content; without this the content would be silently dropped.
func (m *TurnMapper) closeBlock(delta map[string]any, kind ahptypes.ResponsePartKind) []ahptypes.StateAction {
	idx := intOr(delta["contentIndex"], 0)
	if p := m.parts[idx]; p != nil && p.created {
		return nil
	}
	content, _ := delta["content"].(string)
	return m.appendTextValue(idx, content, kind)
}

func (m *TurnMapper) partID(idx int) string {
	return m.turnID + ":" + strconv.Itoa(m.messageOrdinal) + ":" + strconv.Itoa(idx)
}

func (m *TurnMapper) appendText(delta map[string]any, kind ahptypes.ResponsePartKind, _ bool) []ahptypes.StateAction {
	text, _ := delta["delta"].(string)
	return m.appendTextValue(intOr(delta["contentIndex"], 0), text, kind)
}

// appendTextValue appends streamed text, creating the part on first use. The first chunk rides
// along on chat/responsePart rather than a separate chat/delta, which halves the actions for
// short responses without changing the reduced state.
func (m *TurnMapper) appendTextValue(idx int, text string, kind ahptypes.ResponsePartKind) []ahptypes.StateAction {
	if len(text) == 0 {
		return nil
	}
	out := m.setActivity(strPtr(RespondingActivity))
	part := m.parts[idx]
	if part == nil {
		part = &openPart{partID: m.partID(idx), kind: kind}
		m.parts[idx] = part
	}
	if !part.created {
		part.created = true
		var rp ahptypes.ResponsePart
		if kind == ahptypes.ResponsePartKindMarkdown {
			rp = ahptypes.ResponsePart{Value: &ahptypes.MarkdownResponsePart{Kind: kind, Id: part.partID, Content: text}}
		} else {
			rp = ahptypes.ResponsePart{Value: &ahptypes.ReasoningResponsePart{Kind: kind, Id: part.partID, Content: text}}
		}
		return append(out, ahptypes.StateAction{Value: &ahptypes.ChatResponsePartAction{Type: ahptypes.ActionTypeChatResponsePart, TurnId: m.turnID, Part: rp}})
	}
	if kind == ahptypes.ResponsePartKindMarkdown {
		return append(out, ahptypes.StateAction{Value: &ahptypes.ChatDeltaAction{Type: ahptypes.ActionTypeChatDelta, TurnId: m.turnID, PartId: part.partID, Content: text}})
	}
	return append(out, ahptypes.StateAction{Value: &ahptypes.ChatReasoningAction{Type: ahptypes.ActionTypeChatReasoning, TurnId: m.turnID, PartId: part.partID, Content: text}})
}

// ── Tool calls ──────────────────────────────────────────────────────────

func (m *TurnMapper) onToolCallStart(delta map[string]any) []ahptypes.StateAction {
	idx := intOr(delta["contentIndex"], 0)
	block := asMap(dig(delta, "partial", "content", idx))
	id, _ := block["id"].(string)
	if id == "" {
		// Without an id there is nothing to correlate later; wait for the next delta, which
		// carries the resolved block.
		return nil
	}
	m.toolCallsByIdx[idx] = id
	name := "tool"
	if s, ok := block["name"].(string); ok {
		name = s
	}
	return []ahptypes.StateAction{ahptypes.StateAction{Value: &ahptypes.ChatToolCallStartAction{
		Type: ahptypes.ActionTypeChatToolCallStart, TurnId: m.turnID, ToolCallId: id, ToolName: name, DisplayName: name,
	}}}
}

func (m *TurnMapper) onToolCallDelta(delta map[string]any) []ahptypes.StateAction {
	idx := intOr(delta["contentIndex"], 0)
	content, _ := delta["delta"].(string)
	deltaAction := func(id string) ahptypes.StateAction {
		return ahptypes.StateAction{Value: &ahptypes.ChatToolCallDeltaAction{Type: ahptypes.ActionTypeChatToolCallDelta, TurnId: m.turnID, ToolCallId: id, Content: &content}}
	}
	if id, ok := m.toolCallsByIdx[idx]; ok && id != "" {
		return []ahptypes.StateAction{deltaAction(id)}
	}
	// The id may only have materialised after toolcall_start; recover from the partial message
	// rather than dropping the call.
	started := m.onToolCallStart(delta)
	id := m.toolCallsByIdx[idx]
	if id == "" {
		return nil
	}
	return append(started, deltaAction(id))
}

// onToolCallReady: parameters are complete. `confirmed: 'setting'` sends the call straight to
// running, skipping pending-confirmation. That is a first-class path in the protocol and the
// honest one here: pi has no permission system, so there is nothing for a client to approve.
// Because the call never enters pending-confirmation a conforming client renders no approve/deny
// UI; cancelling the whole turn stays available and maps to abort().
func (m *TurnMapper) onToolCallReady(delta map[string]any) []ahptypes.StateAction {
	idx := intOr(delta["contentIndex"], 0)
	toolCall := asMap(delta["toolCall"])
	id, hasID := toolCall["id"].(string)
	if !hasID {
		id = m.toolCallsByIdx[idx]
	}
	if id == "" {
		return nil
	}
	m.toolCallsByIdx[idx] = id
	name := "tool"
	if s, ok := toolCall["name"].(string); ok {
		name = s
	}
	args := toolCall["arguments"]
	cwd := m.opts.WorkingDirectory
	description := DescribeToolCall(name, args, cwd)
	m.liveToolCalls[id] = DescribeFinishedToolCall(name, args, cwd)
	confirmed := ahptypes.ToolCallConfirmationReasonSetting
	ready := &ahptypes.ChatToolCallReadyAction{
		Type: ahptypes.ActionTypeChatToolCallReady, TurnId: m.turnID, ToolCallId: id,
		// Rendered while the call runs, and the same phrasing the activity indicator uses, so the
		// two do not describe one call differently.
		InvocationMessage: ahptypes.NewStringOrMarkdownPlain(description),
		Confirmed:         &confirmed,
	}
	if input, ok := ToolInputFor(name, args); ok {
		ready.ToolInput = &ahptypes.ToolInput{Inline: &input}
	}
	return []ahptypes.StateAction{ahptypes.StateAction{Value: ready}}
}

func (m *TurnMapper) onToolUpdate(event Event) []ahptypes.StateAction {
	id, _ := event["toolCallId"].(string)
	if _, live := m.liveToolCalls[id]; !live {
		return nil
	}
	content := toolResultContent(event["partialResult"])
	if content == nil {
		return nil
	}
	return []ahptypes.StateAction{ahptypes.StateAction{Value: &ahptypes.ChatToolCallContentChangedAction{
		Type: ahptypes.ActionTypeChatToolCallContentChanged, TurnId: m.turnID, ToolCallId: id, Content: content,
	}}}
}

func textContent(text string) []ahptypes.ToolResultContent {
	return []ahptypes.ToolResultContent{{Value: &ahptypes.ToolResultTextContent{Type: ahptypes.ToolResultContentTypeText, Text: text}}}
}

// toolResultContent flattens pi's tool result content blocks into the protocol's text blocks.
func toolResultContent(result any) []ahptypes.ToolResultContent {
	blocks, ok := dig(result, "content").([]any)
	if !ok {
		return nil
	}
	var texts []string
	for _, b := range blocks {
		if bm := asMap(b); bm["type"] == "text" {
			if s, ok := bm["text"].(string); ok && s != "" {
				texts = append(texts, s)
			}
		}
	}
	if len(texts) == 0 {
		return nil
	}
	return textContent(strings.Join(texts, "\n"))
}

// editPatch is the unified diff pi computes for an edit, if the result carries one. `edit` is the
// only built-in tool that reports structured details; its text says how many blocks were replaced
// (a count a client cannot render or check), and the patch beside it is the part worth showing.
func editPatch(result any) (string, bool) {
	s, ok := digStr(result, "details", "patch")
	return s, ok && s != ""
}

// textOf is the text a tool reported, as a client would render it.
func textOf(content []ahptypes.ToolResultContent) string {
	var sb strings.Builder
	for _, c := range content {
		if t, ok := c.Value.(*ahptypes.ToolResultTextContent); ok {
			sb.WriteString(t.Text)
		}
	}
	return trimJS(sb.String())
}

func (m *TurnMapper) onToolEnd(event Event) []ahptypes.StateAction {
	id, _ := event["toolCallId"].(string)
	finished, live := m.liveToolCalls[id]
	if !live {
		return nil
	}
	delete(m.liveToolCalls, id)
	toolName, _ := event["toolName"].(string)
	success := event["isError"] != true
	var content []ahptypes.ToolResultContent
	if patch, ok := editPatch(event["result"]); ok {
		content = textContent(patch)
	} else {
		content = toolResultContent(event["result"])
	}
	// A failed tool has already said why: an ENOENT naming the path it could not open, a non-zero
	// exit with its stderr. That text belongs in error.message, where a client looks.
	reported := textOf(content)
	var past string
	switch {
	case success && live && finished != "":
		past = finished
	case success:
		past = "Ran " + toolName
	case finished != "":
		past = finished + " failed"
	default:
		past = toolName + " failed"
	}
	result := ahptypes.ToolCallResult{Success: success, PastTenseMessage: ahptypes.NewStringOrMarkdownPlain(past), Content: content}
	if !success {
		message := reported
		if message == "" {
			message = toolName + " failed"
		}
		// ToolCallResult.error is its own shape (message + code), not ErrorInfo.
		raw, _ := json.Marshal(map[string]string{"message": message})
		rm := json.RawMessage(raw)
		result.Error = &rm
	}
	return []ahptypes.StateAction{ahptypes.StateAction{Value: &ahptypes.ChatToolCallCompleteAction{
		Type: ahptypes.ActionTypeChatToolCallComplete, TurnId: m.turnID, ToolCallId: id, Result: result,
	}}}
}

// ── Turn termination ────────────────────────────────────────────────────

// Finish closes the turn. It is also called directly when a turn ends without agent_settled: an
// extension command that never reaches the model, or a failed prompt(). Without it the turn would
// stay active forever and the session would sit at InProgress. outcome "" lets the mapper decide;
// message "" means no explicit error message.
func (m *TurnMapper) Finish(outcome Outcome, message string) []ahptypes.StateAction {
	if m.finished {
		return nil
	}
	m.finished = true
	// Nothing is happening once the turn is over; a stale description would leave the client
	// showing "Reading ..." against an idle chat.
	out := m.setActivity(nil)
	resolved := outcome
	if resolved == OutcomeDefault {
		resolved = m.outcome
	}
	duration := m.duration()
	switch resolved {
	case OutcomeCancelled:
		return append(out, ahptypes.StateAction{Value: &ahptypes.ChatTurnCancelledAction{Type: ahptypes.ActionTypeChatTurnCancelled, TurnId: m.turnID, Duration: duration}})
	case OutcomeError:
		msg := "The agent run failed"
		switch {
		case message != "":
			msg = message
		case m.errorMessage != nil:
			msg = *m.errorMessage
		}
		return append(out, ahptypes.StateAction{Value: &ahptypes.ChatErrorAction{
			Type: ahptypes.ActionTypeChatError, TurnId: m.turnID, Duration: duration,
			Part: ahptypes.ErrorResponsePart{Kind: ahptypes.ResponsePartKindError, Error: ahptypes.ErrorInfo{ErrorType: "agentRunFailed", Message: msg}},
		}})
	default:
		return append(out, ahptypes.StateAction{Value: &ahptypes.ChatTurnCompleteAction{Type: ahptypes.ActionTypeChatTurnComplete, TurnId: m.turnID, Duration: duration}})
	}
}

// UserTurnStarted builds the chat/turnStarted action for a user message.
func UserTurnStarted(turnID, text, startedAt string) ahptypes.StateAction {
	return ahptypes.StateAction{Value: &ahptypes.ChatTurnStartedAction{
		Type: ahptypes.ActionTypeChatTurnStarted, TurnId: turnID, StartedAt: startedAt,
		Message: ahptypes.Message{Text: text, Origin: ahptypes.MessageOrigin{Kind: ahptypes.MessageKindUser}},
	}}
}
