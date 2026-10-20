package pi_subagents

import (
	"errors"
	"fmt"
	"strings"
)

// A small tool-calling agent loop: what the reviewer and the permission arbiter run on pi-agent-core's Agent in the original.
// One model request per step through a wdStreamFn; the tool calls of a reply run in order; the loop ends on a reply without
// tool calls, an error or aborted reply, or the abort of the agent.

type wdAgentTool struct {
	Spec wdToolSpec
	// Execute returns the text of the result, or an error that becomes a failed result.
	Execute func(args *jsObject) (string, error)
}

type wdAgent struct {
	systemPrompt  string
	model         map[string]any
	thinkingLevel string
	tools         []wdAgentTool
	streamFn      wdStreamFn
	apiKey        string
	// beforeToolCall returns a reason to block a call.
	beforeToolCall func(name string) (reason string, block bool)
	abortCtl       *wdAbort
	messages       []map[string]any
}

func newWdAgent(systemPrompt string, model map[string]any, thinkingLevel string, tools []wdAgentTool, streamFn wdStreamFn) *wdAgent {
	return &wdAgent{systemPrompt: systemPrompt, model: model, thinkingLevel: thinkingLevel, tools: tools, streamFn: streamFn, abortCtl: newWdAbort()}
}

func (a *wdAgent) abort() { a.abortCtl.abort() }

func (a *wdAgent) aborted() bool { return wdClosed(a.abortCtl.signal()) }

func (a *wdAgent) specs() []wdToolSpec {
	out := make([]wdToolSpec, len(a.tools))
	for i, t := range a.tools {
		out[i] = t.Spec
	}
	return out
}

func wdTextBlocks(text string) []any {
	return []any{map[string]any{"type": "text", "text": text}}
}

// prompt runs the loop for one user prompt.
func (a *wdAgent) prompt(text string) {
	a.messages = append(a.messages, map[string]any{"role": "user", "content": wdTextBlocks(text)})
	for !a.aborted() {
		reasoning := a.thinkingLevel
		if reasoning == "off" {
			reasoning = ""
		}
		reply := a.streamFn(a.model, wdLLMContext{SystemPrompt: a.systemPrompt, Messages: append([]map[string]any{}, a.messages...), Tools: a.specs()},
			wdStreamOptions{APIKey: a.apiKey, Reasoning: reasoning, Signal: a.abortCtl.signal()})
		if reply == nil {
			return
		}
		a.messages = append(a.messages, reply)
		if reason, _ := reply["stopReason"].(string); reason == "error" || reason == "aborted" {
			return
		}
		var calls []map[string]any
		if content, ok := reply["content"].([]any); ok {
			for _, b := range content {
				if block, ok := b.(map[string]any); ok && block["type"] == "toolCall" {
					calls = append(calls, block)
				}
			}
		}
		if len(calls) == 0 {
			return
		}
		for _, call := range calls {
			if a.aborted() {
				return
			}
			a.messages = append(a.messages, a.runToolCall(call))
		}
	}
}

func (a *wdAgent) runToolCall(call map[string]any) map[string]any {
	id, _ := call["id"].(string)
	name, _ := call["name"].(string)
	result := func(text string, isError bool) map[string]any {
		return map[string]any{"role": "toolResult", "toolCallId": id, "toolName": name, "content": wdTextBlocks(text), "isError": isError}
	}
	if a.beforeToolCall != nil {
		if reason, block := a.beforeToolCall(name); block {
			return result(reason, true)
		}
	}
	var tool *wdAgentTool
	for i := range a.tools {
		if a.tools[i].Spec.Name == name {
			tool = &a.tools[i]
		}
	}
	if tool == nil {
		return result("Tool "+name+" not found", true)
	}
	args, _ := toDyn(call["arguments"]).(*jsObject)
	if args == nil {
		args = newObject()
	}
	if violations := schemaViolations(toDyn(tool.Spec.Parameters), args); len(violations) > 0 {
		return result("Validation failed for tool \""+name+"\":\n  - "+strings.Join(violations, "\n  - "), true)
	}
	text, err := tool.Execute(args)
	if err != nil {
		return result(err.Error(), true)
	}
	return result(text, false)
}

// lastAssistant is the last assistant message of the transcript.
func (a *wdAgent) lastAssistant() map[string]any {
	for i := len(a.messages) - 1; i >= 0; i-- {
		if a.messages[i]["role"] == "assistant" {
			return a.messages[i]
		}
	}
	return nil
}

// wdStopReason maps the terminal assistant message of a run to the review's stop reason.
func wdStopReason(terminal map[string]any) (reason, errorMessage string) {
	r, _ := terminal["stopReason"].(string)
	switch r {
	case "error", "aborted", "length":
		reason = r
	default:
		reason = "stop"
	}
	if s, ok := terminal["errorMessage"].(string); ok {
		errorMessage = s
	}
	return reason, errorMessage
}

var errWdNoAssistant = errors.New("the model returned no message")

func wdToolSchema(properties map[string]any, required []string) map[string]any {
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func wdEnumList(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

var _ = fmt.Sprintf
