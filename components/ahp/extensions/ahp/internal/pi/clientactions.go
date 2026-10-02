package pi

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/mapper"
)

// unsupportedClientActionReason is the policy for client-dispatchable actions this host does not
// carry out. Refusing is the honest answer: silently accepting would move client state away from
// what the agent does.
func unsupportedClientActionReason(actionType string) string {
	t := func(a ahptypes.ActionType) string { return string(a) }
	switch actionType {
	case t(ahptypes.ActionTypeSessionActiveClientSet), t(ahptypes.ActionTypeSessionActiveClientRemoved):
		return "This host does not accept active clients"
	case t(ahptypes.ActionTypeSessionWorkingDirectorySet), t(ahptypes.ActionTypeSessionWorkingDirectoryRemoved),
		t(ahptypes.ActionTypeSessionWorkingDirectoryReplaced), t(ahptypes.ActionTypeChatWorkingDirectorySet),
		t(ahptypes.ActionTypeChatWorkingDirectoryRemoved):
		return "This agent does not support changing working directories"
	case t(ahptypes.ActionTypeSessionCustomizationToggled):
		return "This host does not support customizations"
	case t(ahptypes.ActionTypeSessionMcpServerStartRequested), t(ahptypes.ActionTypeSessionMcpServerStopRequested),
		// Not in the upstream policy: the pinned spec commit is newer than the npm 0.9.0 package
		// pi-ahp was written against, and added these two client-dispatchable actions. Refused
		// like their siblings (see PORT.md).
		t(ahptypes.ActionTypeSessionMcpServerBackgroundRequested):
		return "This host does not support MCP servers"
	case t(ahptypes.ActionTypeSessionIsReadChanged), t(ahptypes.ActionTypeSessionIsArchivedChanged),
		t(ahptypes.ActionTypeChatIsArchivedChanged): // chat/isArchivedChanged: newer than upstream's spec, see above
		return "This host does not persist read or archive state"
	case t(ahptypes.ActionTypeSessionConfigChanged):
		return "This session has no mutable configuration"
	case t(ahptypes.ActionTypeChatToolCallConfirmed), t(ahptypes.ActionTypeChatToolCallComplete),
		t(ahptypes.ActionTypeChatToolCallResultConfirmed), t(ahptypes.ActionTypeChatToolCallContentChanged):
		return "This host does not support client tool execution or confirmation"
	case t(ahptypes.ActionTypeChatInputAnswerChanged), t(ahptypes.ActionTypeChatInputCompleted):
		return "This host does not support interactive input requests"
	case t(ahptypes.ActionTypeChatTurnResume):
		return "This host cannot resume an errored turn"
	}
	return ""
}

// userMessageRejectionReason is the message check plus the requirement that a client speaks as a
// user. The message is raw decoded JSON, so a malformed shape is reported rather than trusted.
func userMessageRejectionReason(message any, originReason string) string {
	if reason := mapper.MessageRejectionReason(message); reason != "" {
		return reason
	}
	m, _ := message.(map[string]any)
	origin, _ := m["origin"].(map[string]any)
	if origin["kind"] == string(ahptypes.MessageKindUser) {
		return ""
	}
	return originReason
}

func (r *Registry) validateClientAction(channel string, action host.ClientAction) string {
	r.mu.Lock()
	session := r.sessions[channel]
	if session == nil {
		session = r.byChat[channel]
	}
	_, channelDisposing := r.disposals[channel]
	disposing := channelDisposing
	if session != nil {
		_, sessionDisposing := r.disposals[session.URI]
		disposing = disposing || sessionDisposing
	}
	r.mu.Unlock()
	if disposing {
		return "This session is being disposed"
	}
	if reason := unsupportedClientActionReason(action.Type); reason != "" {
		return reason
	}

	var raw map[string]any
	dec := json.NewDecoder(bytes.NewReader(action.Raw))
	dec.UseNumber()
	_ = dec.Decode(&raw)
	chat := r.host.Store().Chat(channel)
	str := func(key string) (string, bool) { s, ok := raw[key].(string); return s, ok }

	switch action.Type {
	case string(ahptypes.ActionTypeSessionTitleChanged):
		if _, ok := str("title"); !ok {
			return "A session title must be a string"
		}
	case string(ahptypes.ActionTypeChatTurnStarted):
		_, okID := str("turnId")
		_, okAt := str("startedAt")
		if !okID || !okAt {
			return "A turn requires string turnId and startedAt fields"
		}
		if reason := userMessageRejectionReason(plain(raw["message"]), "A client can only start a turn with a user message"); reason != "" {
			return reason
		}
		if _, present := raw["queuedMessageId"]; present {
			return "Only the host can start a queued message"
		}
		if chat != nil && chat.ActiveTurn != nil {
			return "A turn is already active"
		}
	case string(ahptypes.ActionTypeChatTurnCancelled):
		id, okID := str("turnId")
		_, okDuration := raw["duration"].(json.Number)
		if !okID || !okDuration {
			return "Turn cancellation requires a turnId and duration"
		}
		if chat == nil || chat.ActiveTurn == nil || chat.ActiveTurn.Id != id {
			return "No matching active turn to cancel"
		}
	case string(ahptypes.ActionTypeChatPendingMessageSet):
		_, okID := str("id")
		kind, _ := str("kind")
		if !okID || (kind != string(ahptypes.PendingMessageKindSteering) && kind != string(ahptypes.PendingMessageKindQueued)) {
			return "A pending message requires an id and supported kind"
		}
		return userMessageRejectionReason(plain(raw["message"]), "A client can only queue a user message")
	case string(ahptypes.ActionTypeChatPendingMessageRemoved):
		id, okID := str("id")
		if !okID {
			return "A pending message removal requires an id"
		}
		kind, _ := str("kind")
		if kind == string(ahptypes.PendingMessageKindSteering) {
			return "A steering message cannot be withdrawn after pi has queued it"
		}
		if kind != string(ahptypes.PendingMessageKindQueued) {
			return "Unsupported pending message kind"
		}
		if chat != nil {
			for _, m := range chat.QueuedMessages {
				if m.Id == id {
					return ""
				}
			}
		}
		return "No matching queued message to remove"
	case string(ahptypes.ActionTypeChatQueuedMessagesReordered):
		order, ok := raw["order"].([]any)
		if ok {
			for _, id := range order {
				if _, isStr := id.(string); !isStr {
					ok = false
				}
			}
		}
		if !ok {
			return "Queued message order must be an array of ids"
		}
	case string(ahptypes.ActionTypeChatDraftChanged):
		draft, present := raw["draft"]
		if !present {
			return ""
		}
		return userMessageRejectionReason(plain(draft), "A client can only draft a user message")
	case string(ahptypes.ActionTypeChatTruncated):
		var turnID *string
		if v, present := raw["turnId"]; present {
			s, ok := v.(string)
			if !ok {
				return "A truncation turnId must be a string"
			}
			turnID = &s
		}
		// Accepting an impossible truncation would shorten the client's view while Pi kept using
		// context the user believes is gone.
		owner, ok := r.GetByChat(channel)
		if !ok {
			return ""
		}
		if _, ok := TruncationAnchor(owner, turnID); !ok {
			label := "(all)"
			if turnID != nil {
				label = *turnID
			}
			return fmt.Sprintf("Cannot truncate: no session entry matches turn %s", label)
		}
	}
	return ""
}

// plain converts json.Number values to float64 so the shared message checks (written over
// standard decoded JSON) see numbers as they expect.
func plain(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = plain(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = plain(e)
		}
		return out
	case json.Number:
		f, _ := x.Float64()
		return f
	}
	return v
}
