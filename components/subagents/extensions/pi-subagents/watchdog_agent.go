package pi_subagents

import (
	"errors"
	"fmt"
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
	panic("not implemented")
}

func (a *wdAgent) abort() { panic("not implemented") }

func (a *wdAgent) aborted() bool { panic("not implemented") }

func (a *wdAgent) specs() []wdToolSpec { panic("not implemented") }

func wdTextBlocks(text string) []any { panic("not implemented") }

// prompt runs the loop for one user prompt.
func (a *wdAgent) prompt(text string) { panic("not implemented") }

func (a *wdAgent) runToolCall(call map[string]any) map[string]any { panic("not implemented") }

// lastAssistant is the last assistant message of the transcript.
func (a *wdAgent) lastAssistant() map[string]any { panic("not implemented") }

// wdStopReason maps the terminal assistant message of a run to the review's stop reason.
func wdStopReason(terminal map[string]any) (reason, errorMessage string) { panic("not implemented") }

var errWdNoAssistant = errors.New("the model returned no message")

func wdToolSchema(properties map[string]any, required []string) map[string]any {
	panic("not implemented")
}

func wdEnumList(values []string) []any { panic("not implemented") }

var _ = fmt.Sprintf
