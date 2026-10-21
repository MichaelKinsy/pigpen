package tintinweb_subagents

import (
	"context"
	"errors"
	"fmt"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Start a mentioned agent through a clone of this conversation, without putting anything in the chat. Claude Code
// routes `@agent-<type>` through the main model, which costs a visible turn for a decision the user already made. So
// the turn happens somewhere else: the conversation (as the session resolves it, compaction applied) is copied into a
// throwaway request, with the live system prompt and the session's model, and that copy takes the turn off-screen.
// The copy has one tool, Agent, which spawns top-level in the real session, in the background, with no tool-call id,
// and at most once per mention. upstream: src/mention-clone.ts.
//
// The original builds a throwaway in-process pi session (createAgentSession) and prompts it. A Go extension has no
// in-process session, so the same turn runs through the host's model API: the messages are the session's own,
// the request carries the one tool, and the loop answers each tool call as Pi's agent loop would (port/PORT.md).

// maxCloneTurns bounds the clone's model turns. Pi's loop has no cap; a clone whose model keeps calling tools after
// its one spawn would otherwise spend tokens where nobody looks.
const maxCloneTurns = 8

// cloneHost is what the clone needs of the main session.
type cloneHost interface {
	// conversation is the session's messages as it resolves them (buildSessionContext): compaction applied, branch
	// summaries substituted. Taken from memory, never from the session file, which is written only after the first
	// assistant message.
	conversation() ([]map[string]any, error)
	systemPrompt() string
	// turn runs one model turn over the request ({systemPrompt, messages, tools}) and returns the assistant message.
	turn(ctx context.Context, request map[string]any) (map[string]any, error)
}

// cloneTool is the Agent tool as the clone's model sees it, and the handler the real session registered.
type cloneTool struct {
	Name        string
	Description string
	Parameters  any
	// Run is the registered handler, bound to the MAIN context: it reads cwd, model and the session id off it, and a
	// clone's own context would file the transcript under the throwaway. It is called with no tool-call id.
	Run func(params map[string]any) (text string, isError bool)
}

type mentionCloneOptions struct {
	Host    cloneHost
	Type    string
	Message string
	Tool    cloneTool
}

// mentionCloneResult: Spawned is true once the clone actually called Agent; Error says why not, or what failed after.
type mentionCloneResult struct {
	Spawned bool
	Error   string
}

func textMessage(role, text string) map[string]any {
	return map[string]any{"role": role, "content": []any{map[string]any{"type": "text", "text": text}}, "timestamp": time.Now().UnixMilli()}
}

// runMentionClone lets the copy make the tool call. It never fails: a clone that cannot run is reported, so the
// caller can fall back to starting the agent directly, and a panic is a failure like any other.
func runMentionClone(ctx context.Context, o mentionCloneOptions) (res mentionCloneResult) {
	spawned := false
	defer func() {
		if v := recover(); v != nil {
			res = mentionCloneResult{Spawned: spawned, Error: fmt.Sprint(v)}
		}
	}()
	fail := func(err error) mentionCloneResult { return mentionCloneResult{Spawned: spawned, Error: err.Error()} }

	conversation, err := o.Host.conversation()
	if err != nil {
		return fail(err)
	}
	messages := append(make([]any, 0, len(conversation)+2*maxCloneTurns), toAnySlice(conversation)...)
	// User text first, reminder after: the order Claude Code's attachment renderer produces.
	messages = append(messages, textMessage("user", o.Message+"\n\n"+agentMentionReminder(o.Type)))
	tools := []any{map[string]any{"name": o.Tool.Name, "description": o.Tool.Description, "parameters": o.Tool.Parameters}}

	for turn := 0; turn < maxCloneTurns; turn++ {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		reply, err := o.Host.turn(ctx, map[string]any{"systemPrompt": o.Host.systemPrompt(), "messages": messages, "tools": tools})
		if err != nil {
			return fail(err)
		}
		messages = append(messages, reply)
		if reason, _ := reply["stopReason"].(string); reason == "error" || reason == "aborted" {
			msg, _ := reply["errorMessage"].(string)
			if msg == "" {
				msg = "the clone's turn " + reason
			}
			return fail(errors.New(msg))
		}
		calls := toolCalls(reply)
		if len(calls) == 0 {
			break
		}
		for _, call := range calls {
			text, isError := "", false
			switch {
			case call.name != o.Tool.Name:
				text, isError = "Tool "+call.name+" not found", true
			case spawned:
				// One spawn per mention: a model that decides to "also" launch a second agent would do it where nobody
				// can see and nobody asked.
				text, isError = "Already started an agent for this mention. Stop here.", true
			default:
				spawned = true
				params := map[string]any{}
				for k, v := range call.args {
					params[k] = v
				}
				params["run_in_background"] = true // a foreground answer would go to the discarded clone
				text, isError = o.Tool.Run(params)
			}
			result := textMessage("toolResult", text)
			result["toolCallId"], result["toolName"], result["isError"] = call.id, call.name, isError
			messages = append(messages, result)
		}
	}
	if spawned {
		return mentionCloneResult{Spawned: true}
	}
	return mentionCloneResult{Error: "the conversation clone did not start it"}
}

type toolCall struct {
	id, name string
	args     map[string]any
}

func toolCalls(msg map[string]any) []toolCall {
	var out []toolCall
	blocks, _ := msg["content"].([]any)
	for _, b := range blocks {
		m, _ := b.(map[string]any)
		if m["type"] != "toolCall" {
			continue
		}
		c := toolCall{args: map[string]any{}}
		c.id, _ = m["id"].(string)
		c.name, _ = m["name"].(string)
		if a, ok := m["arguments"].(map[string]any); ok {
			c.args = a
		}
		out = append(out, c)
	}
	return out
}

func toAnySlice(in []map[string]any) []any {
	out := make([]any, len(in))
	for i, m := range in {
		out[i] = m
	}
	return out
}

// sessionCloneHost is the main session as a cloneHost.
type sessionCloneHost struct{ ctx sdk.Context }

func (h sessionCloneHost) conversation() ([]map[string]any, error) {
	sc, err := h.ctx.SessionManager().BuildSessionContext()
	if err != nil {
		return nil, err
	}
	return sc.Messages, nil
}

func (h sessionCloneHost) systemPrompt() string {
	p, _ := h.ctx.GetSystemPrompt()
	return p
}

func (h sessionCloneHost) turn(ctx context.Context, request map[string]any) (map[string]any, error) {
	reg := h.ctx.ModelRegistry()
	model := reg.Find(h.ctx.ModelProvider(), h.ctx.Model())
	if model == nil {
		return nil, errors.New("no model is selected")
	}
	options := map[string]any{}
	auth, err := reg.GetApiKeyAndHeaders(model)
	if err != nil {
		return nil, err
	}
	if ok, present := auth["ok"].(bool); present && !ok {
		msg, _ := auth["error"].(string)
		return nil, errors.New("no credentials for the model: " + msg)
	}
	if key, _ := auth["apiKey"].(string); key != "" {
		options["apiKey"] = key
	}
	if headers := auth["headers"]; headers != nil {
		options["headers"] = headers
	}
	// The level the session is really on (buildSessionContext's own reads "off" until someone ran /think).
	if level, err := h.ctx.GetThinkingLevel(); err == nil && level != "" && level != "off" {
		options["reasoning"] = level
	}
	type outcome struct{ msg map[string]any }
	done := make(chan outcome, 1)
	goGuarded("clone model turn", func() {
		var msg map[string]any
		defer func() { done <- outcome{msg} }() // a panic still answers, with nothing
		msg = reg.StreamSimple(model, request, options).Result()
	})
	select {
	case o := <-done:
		if o.msg == nil {
			return nil, errors.New("the host returned no model result")
		}
		return o.msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
