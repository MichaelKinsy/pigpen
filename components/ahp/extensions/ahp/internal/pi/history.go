package pi

import (
	"strings"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/mapper"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pisession"
)

// Rebuilds chat history from a pi session file (port of src/pi/history.ts).
//
// A session from the catalogue exists only on disk, so subscribing to it has to reconstruct its
// state, or the client opens an empty conversation. The window matches what pi itself renders:
// the most recent compaction onward (ContextEntries), which is the transcript pi's own TUI shows
// on resume; reconstructing more would show the user history their agent has already forgotten.
//
// This is a second mapper, distinct from the streaming one: it works from finished messages
// rather than deltas, so there is no partial state and every tool call already has its result.

// ClearAllAnchor is the key under which the whole-conversation anchor is stored.
const ClearAllAnchor = ""

// RebuiltHistory is the result of a rebuild.
type RebuiltHistory struct {
	Turns []ahptypes.Turn
	// Anchors maps a turn id to the session entry that turn ends on: what truncation needs.
	// chat/truncated keeps turns up to and including the named one, and pointing the agent at a
	// turn's last entry expresses exactly that. The first user entry is recorded under
	// ClearAllAnchor: navigating to it lands the leaf on its parent (null), which is how "clear
	// everything" is expressed.
	Anchors map[string]string
}

// RebuildOptions configure a rebuild.
type RebuildOptions struct {
	// TurnIDPrefix prefixes generated turn ids; it only needs to be stable within a chat.
	TurnIDPrefix string
}

func blocksOf(message map[string]any) []map[string]any {
	if s, ok := message["content"].(string); ok {
		return []map[string]any{{"type": "text", "text": s}}
	}
	var out []map[string]any
	if arr, ok := message["content"].([]any); ok {
		for _, b := range arr {
			if m, ok := b.(map[string]any); ok {
				out = append(out, m)
			}
		}
	}
	return out
}

func plainText(message map[string]any) string {
	var sb strings.Builder
	for _, b := range blocksOf(message) {
		if b["type"] == "text" {
			if s, ok := b["text"].(string); ok {
				sb.WriteString(s)
			}
		}
	}
	return sb.String()
}

func numberPtr(v any) *int64 {
	switch n := v.(type) {
	case float64:
		i := int64(n)
		return &i
	}
	return nil
}

func storedUsage(message map[string]any) *ahptypes.UsageInfo {
	usage, ok := message["usage"].(map[string]any)
	if !ok {
		return nil
	}
	info := &ahptypes.UsageInfo{
		InputTokens: numberPtr(usage["input"]), OutputTokens: numberPtr(usage["output"]), CacheReadTokens: numberPtr(usage["cacheRead"]),
	}
	if model, _ := message["model"].(string); model != "" {
		info.Model = &model
	}
	return info
}

func textResult(text string) []ahptypes.ToolResultContent {
	return []ahptypes.ToolResultContent{{Value: &ahptypes.ToolResultTextContent{Type: ahptypes.ToolResultContentTypeText, Text: text}}}
}

type pendingTurn struct {
	id, startedAt string
	message       ahptypes.Message
	parts         []ahptypes.ResponsePart
	usage         *ahptypes.UsageInfo
}

func stringOr(v any, def string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return def
}

// RebuildHistory converts session entries into completed turns.
//
// Turn boundaries come from user messages: each one opens a turn that absorbs every assistant
// message and tool result until the next. That mirrors the live mapper, where a turn spans
// everything from one prompt to agent_settled.
func RebuildHistory(entries []pisession.Entry, opts RebuildOptions) RebuiltHistory {
	prefix := opts.TurnIDPrefix
	if prefix == "" {
		prefix = "turn"
	}
	turns := []ahptypes.Turn{}
	anchors := map[string]string{}
	var lastEntryID string
	var current *pendingTurn
	toolCallParts := map[string]int{}

	closeTurn := func() {
		if current == nil {
			return
		}
		if lastEntryID != "" {
			anchors[current.id] = lastEntryID
		}
		started := current.startedAt
		turns = append(turns, ahptypes.Turn{
			Id: current.id, StartedAt: &started, Message: current.message,
			ResponseParts: current.parts, Usage: current.usage, State: ahptypes.TurnStateComplete,
		})
		current = nil
		toolCallParts = map[string]int{}
	}
	openTurn := func(id, startedAt string, message ahptypes.Message) {
		closeTurn()
		current = &pendingTurn{id: id, startedAt: startedAt, message: message, parts: []ahptypes.ResponsePart{}}
	}

	for _, entry := range entries {
		if entry.Role() == "user" {
			if _, set := anchors[ClearAllAnchor]; !set {
				anchors[ClearAllAnchor] = entry.ID()
			}
		}

		if entry.Type() == "compaction" {
			// The summary replaces everything before it. Surfacing it as a turn of its own tells
			// the user why the history starts where it does.
			closeTurn()
			started := entry.Timestamp()
			summary, _ := entry["summary"].(string)
			turns = append(turns, ahptypes.Turn{
				Id: prefix + "-compaction-" + entry.ID(), StartedAt: &started,
				Message: ahptypes.Message{Text: "", Origin: ahptypes.MessageOrigin{Kind: ahptypes.MessageKindUser}},
				ResponseParts: []ahptypes.ResponsePart{{Value: &ahptypes.SystemNotificationResponsePart{
					Kind:    ahptypes.ResponsePartKindSystemNotification,
					Content: ahptypes.NewStringOrMarkdownPlain("Conversation compacted.\n\n" + summary),
				}}},
				State: ahptypes.TurnStateComplete,
			})
			continue
		}
		if entry.Type() != "message" {
			continue // model / thinking-level changes and labels carry no transcript
		}

		message := entry.Message()
		switch message["role"] {
		case "user":
			openTurn(prefix+"-"+entry.ID(), entry.Timestamp(), mapper.UserMessageFromPiContent(message["content"]))
		case "assistant":
			if current == nil {
				// An assistant message with no preceding user message (a truncated file, or history
				// that starts mid-turn): give it a turn so the content is not dropped.
				openTurn(prefix+"-"+entry.ID(), entry.Timestamp(), mapper.UserMessageFromPiContent(""))
			}
			for index, block := range blocksOf(message) {
				partID := current.id + ":" + entry.ID() + ":" + itoa(index)
				switch {
				case block["type"] == "text" && stringOr(block["text"], "") != "":
					current.parts = append(current.parts, ahptypes.ResponsePart{Value: &ahptypes.MarkdownResponsePart{
						Kind: ahptypes.ResponsePartKindMarkdown, Id: partID, Content: block["text"].(string)}})
				case block["type"] == "thinking" && stringOr(block["thinking"], "") != "":
					current.parts = append(current.parts, ahptypes.ResponsePart{Value: &ahptypes.ReasoningResponsePart{
						Kind: ahptypes.ResponsePartKindReasoning, Id: partID, Content: block["thinking"].(string)}})
				case block["type"] == "toolCall" && stringOr(block["id"], "") != "":
					id := block["id"].(string)
					name := stringOr(block["name"], "tool")
					toolCallParts[id] = len(current.parts)
					running := &ahptypes.ToolCallRunningState{
						Status: ahptypes.ToolCallStatusRunning, ToolCallId: id, ToolName: name, DisplayName: name,
						InvocationMessage: ahptypes.NewStringOrMarkdownPlain(name), Confirmed: ahptypes.ToolCallConfirmationReasonSetting,
					}
					if args, present := block["arguments"]; present {
						if s, err := mapper.StringifyJSON(args, false); err == nil {
							running.ToolInput = &ahptypes.ToolInput{Inline: &s}
						}
					}
					current.parts = append(current.parts, ahptypes.ResponsePart{Value: &ahptypes.ToolCallResponsePart{
						Kind: ahptypes.ResponsePartKindToolCall, ToolCall: ahptypes.ToolCallState{Value: running}}})
				}
			}
			if u := storedUsage(message); u != nil {
				current.usage = u
			}
		case "toolResult":
			callID, _ := message["toolCallId"].(string)
			idx, found := toolCallParts[callID]
			if current == nil || callID == "" || !found {
				break
			}
			part, ok := current.parts[idx].Value.(*ahptypes.ToolCallResponsePart)
			if !ok {
				break
			}
			running, ok := part.ToolCall.Value.(*ahptypes.ToolCallRunningState)
			if !ok {
				break
			}
			name := stringOr(message["toolName"], "tool")
			success := message["isError"] != true
			past := "Ran " + name
			if !success {
				past = name + " failed"
			}
			done := &ahptypes.ToolCallCompletedState{
				Status: ahptypes.ToolCallStatusCompleted, ToolCallId: running.ToolCallId, ToolName: running.ToolName,
				DisplayName: running.DisplayName, InvocationMessage: running.InvocationMessage, ToolInput: running.ToolInput,
				Confirmed: running.Confirmed, Success: success, PastTenseMessage: ahptypes.NewStringOrMarkdownPlain(past),
			}
			if text := plainText(message); text != "" {
				done.Content = textResult(text)
			}
			current.parts[idx] = ahptypes.ResponsePart{Value: &ahptypes.ToolCallResponsePart{
				Kind: ahptypes.ResponsePartKindToolCall, ToolCall: ahptypes.ToolCallState{Value: done}}}
		case "bashExecution":
			// pi records `!command` runs as their own message role; they are part of the transcript
			// even though no model produced them.
			command, _ := message["command"].(string)
			output, _ := message["output"].(string)
			if current == nil {
				openTurn(prefix+"-"+entry.ID(), entry.Timestamp(), mapper.UserMessageFromPiContent("!"+command))
			}
			current.parts = append(current.parts, ahptypes.ResponsePart{Value: &ahptypes.SystemNotificationResponsePart{
				Kind: ahptypes.ResponsePartKindSystemNotification, Content: ahptypes.NewStringOrMarkdownPlain("`" + command + "`\n\n" + output)}})
		}
		// Recorded after the entry has been folded in, so a user message that opens a new turn
		// does not become the previous turn's anchor.
		lastEntryID = entry.ID()
	}
	closeTurn()
	return RebuiltHistory{Turns: turns, Anchors: anchors}
}

// RebuildHistoryFromSession rebuilds the turns of a session with the window pi's TUI shows.
func RebuildHistoryFromSession(store SessionStore, opts RebuildOptions) RebuiltHistory {
	return RebuildHistory(store.ContextEntries(), opts)
}

func itoa(i int) string {
	return mapper.Itoa(i)
}
