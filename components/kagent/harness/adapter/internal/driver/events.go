package driver

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/MichaelKinsy/pigpen/components/kagent/harness/adapter/internal/config"
	"github.com/kagent-dev/kagent/go/harness/runtime"
)

// Failure texts are fixed strings. Provider and tool text never reaches them:
// a failed model call carries the provider's error text, which can echo
// request content.
const (
	failModelCall = "the model call failed"
	failAborted   = "the turn was aborted"
	failRejected  = "PiG rejected the prompt"
	failPiG       = "PiG reported an error"
)

// translator turns pig events into EventSink calls for one turn.
type translator struct {
	sink    runtime.EventSink
	servers []config.PolicyServer
	now     func() time.Time

	streamed   strings.Builder // text streamed for the current assistant message
	hasText    bool            // the current message streamed any text
	anyText    bool            // some earlier message of this turn produced text
	lastStop   string
	retries    int
	canceling  bool
	toolNames  map[string]string
	called     map[string]bool
	settled    bool
	piError    bool
	modelCalls int
}

func newTranslator(sink runtime.EventSink, servers []config.PolicyServer, now func() time.Time) *translator {
	return &translator{sink: sink, servers: servers, now: now, toolNames: map[string]string{}, called: map[string]bool{}}
}

// handle processes one non-response record. It reports done at agent_settled.
func (t *translator) handle(kind string, raw []byte) (done bool, err error) {
	switch kind {
	case "message_start":
		var rec messageRecord
		if json.Unmarshal(raw, &rec) == nil && rec.Message != nil && rec.Message.Role == "assistant" {
			t.streamed.Reset()
			t.hasText = false
			t.lastStop = ""
		}
	case "message_update":
		var rec messageRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			return false, err
		}
		if e := rec.AssistantMessageEvent; e != nil && e.Type == "text_delta" && !t.canceling {
			return false, t.text(e.Delta)
		}
	case "message_end":
		var rec messageRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			return false, err
		}
		if rec.Message != nil && rec.Message.Role == "assistant" {
			return false, t.assistantEnd(rec.Message)
		}
	case "tool_execution_start":
		var rec toolRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			return false, err
		}
		if !t.canceling {
			return false, t.toolCall(rec.ToolCallID, rec.ToolName, rec.Args)
		}
	case "tool_execution_end":
		var rec toolRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			return false, err
		}
		if !t.canceling {
			return false, t.toolResult(rec)
		}
	case "auto_retry_start":
		t.retries++
	case "error":
		t.piError = true
	case "agent_settled":
		t.settled = true
		return true, nil
	}
	return false, nil
}

func (t *translator) text(delta string) error {
	if delta == "" {
		return nil
	}
	if !t.hasText && t.anyText {
		if err := t.sink.TextDelta(runtime.TextDelta{Text: "\n\n"}); err != nil {
			return err
		}
	}
	t.hasText, t.anyText = true, true
	t.streamed.WriteString(delta)
	return t.sink.TextDelta(runtime.TextDelta{Text: delta})
}

func (t *translator) assistantEnd(m *messageWire) error {
	t.lastStop = m.StopReason
	t.modelCall(m)
	if m.StopReason == "error" || m.StopReason == "aborted" || t.canceling {
		return nil // a failed attempt can retry; never copy provider text
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(m.Content, &blocks) != nil {
		return nil
	}
	var final strings.Builder
	for _, block := range blocks {
		if block.Type == "text" {
			final.WriteString(block.Text)
		}
	}
	streamed := t.streamed.String()
	if text := final.String(); len(text) > len(streamed) && strings.HasPrefix(text, streamed) {
		return t.text(text[len(streamed):])
	}
	return nil
}

// modelCall reports one model request from an assistant message's usage.
func (t *translator) modelCall(m *messageWire) {
	call := runtime.ModelCall{
		Provider: m.Provider, RequestModel: m.Model, ResponseModel: m.Model, ResponseID: m.ResponseID,
		StopReason: m.StopReason, End: t.now(),
	}
	if m.DurationMs > 0 {
		call.Start = call.End.Add(-time.Duration(m.DurationMs) * time.Millisecond)
	} else if m.Timestamp > 0 {
		call.Start = time.UnixMilli(m.Timestamp)
	} else {
		call.Start = call.End
	}
	if u := m.Usage; u != nil {
		// PiG's input excludes cache reads and writes; kagent's includes them.
		call.InputTokens = u.Input + u.CacheRead + u.CacheWrite
		call.CacheReadTokens, call.CacheWriteTokens = u.CacheRead, u.CacheWrite
		call.OutputTokens, call.ReasoningOutputTokens = u.Output, u.Reasoning
	} else {
		call.UsagePartial = true
	}
	switch m.StopReason {
	case "aborted":
		call.Canceled = t.canceling
	case "error":
		call.ErrorType = "provider_error"
	}
	if m.StopReason != "error" {
		call.Retries, t.retries = t.retries, 0
	}
	t.modelCalls++
	t.sink.ModelCall(call)
}

// mcpParts splits mcp__<server>__<tool> against the configured servers.
func (t *translator) mcpParts(name string) (server, tool string, ok bool) {
	return SplitMCPName(t.servers, name)
}

// SplitMCPName splits a PiG MCP tool name into the configured server name and
// the tool part as PiG wrote it. The server part of the name is sanitised.
func SplitMCPName(servers []config.PolicyServer, name string) (server, tool string, ok bool) {
	best := 0
	for _, s := range servers {
		prefix := "mcp__" + SanitizeToolName(s.Name) + "__"
		if strings.HasPrefix(name, prefix) && len(prefix) > best {
			best, server, tool = len(prefix), s.Name, strings.TrimPrefix(name, prefix)
		}
	}
	return server, tool, best > 0
}

func (t *translator) toolCall(id, name string, args json.RawMessage) error {
	if id == "" || name == "" || t.called[id] {
		return nil
	}
	t.called[id] = true
	t.toolNames[id] = name
	call := runtime.ToolCall{ID: id, Name: name, Arguments: decodeArgs(args)}
	if server, _, ok := t.mcpParts(name); ok {
		call.MCPServer, call.MCPTool = server, RawToolName(t.servers, server, name)
	}
	return t.sink.ToolCall(call)
}

func (t *translator) toolResult(rec toolRecord) error {
	if rec.ToolCallID == "" {
		return nil
	}
	name := t.toolNames[rec.ToolCallID]
	if name == "" {
		name = rec.ToolName
	}
	var result any
	if len(rec.Result) > 0 {
		if err := json.Unmarshal(rec.Result, &result); err != nil {
			result = string(rec.Result)
		}
	}
	return t.sink.ToolResult(runtime.ToolResult{ID: rec.ToolCallID, Name: name, Result: result, IsError: rec.IsError})
}

func decodeArgs(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var args map[string]any
	if json.Unmarshal(raw, &args) != nil || args == nil {
		return map[string]any{}
	}
	return args
}

// outcome decides the turn's end state from the last assistant stop reason.
func (t *translator) outcome() runtime.Outcome {
	switch {
	case t.lastStop == "error":
		return runtime.Outcome{Failure: &runtime.Failure{Message: failModelCall}}
	case t.lastStop == "aborted":
		return runtime.Outcome{Failure: &runtime.Failure{Message: failAborted}}
	case t.piError && t.modelCalls == 0:
		return runtime.Outcome{Failure: &runtime.Failure{Message: failPiG}}
	}
	return runtime.Outcome{}
}
