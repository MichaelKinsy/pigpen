package channels

import (
	"encoding/json"
	"time"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// InitialChatState is a fresh, idle chat (port of initialChatState in src/channels/chat.ts).
// Every session this host serves has exactly one chat, which is why the agent declares no
// `multipleChats` capability: its absence tells a client not to call `createChat`.
//
// A selection seeds the draft's model before the agent exists. Starting one takes seconds, and a
// client that subscribes in the meantime would otherwise find an empty model picker and be unable
// to send. The backend refines it once it is up.
func InitialChatState(uri, title string, selection *ahptypes.ModelSelection) *ahptypes.ChatState {
	state := &ahptypes.ChatState{
		Resource:   uri,
		Title:      title,
		Status:     ahptypes.SessionStatusIdle,
		ModifiedAt: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		// A session's default chat exists because the user made the session.
		Origin: &ahptypes.ChatOrigin{Value: &ahptypes.ChatUserOrigin{Kind: ahptypes.ChatOriginKindUser}},
		Turns:  []ahptypes.Turn{},
	}
	if selection != nil {
		state.Draft = &ahptypes.Message{Text: "", Origin: ahptypes.MessageOrigin{Kind: ahptypes.MessageKindUser}, Model: selection}
	}
	return state
}

// ChatSummaryOf is the denormalised summary of a chat that its session carries.
func ChatSummaryOf(state *ahptypes.ChatState) ahptypes.ChatSummary {
	return ahptypes.ChatSummary{
		Resource: state.Resource, Title: state.Title, Status: state.Status, ModifiedAt: state.ModifiedAt,
		Activity: state.Activity, Origin: state.Origin, Interactivity: state.Interactivity,
		WorkingDirectories: state.WorkingDirectories,
	}
}

// InstallDefaultChat creates a session's default chat and registers it in the session catalogue.
// The chat id is derived from the session id so the pairing is reconstructible without a lookup
// table.
func InstallDefaultChat(h *host.Host, sessionChannel, sessionID, title string, selection *ahptypes.ModelSelection) string {
	uri := wire.ChatURI(sessionID)
	h.Store().Create(uri, InitialChatState(uri, title, selection))
	summary := ChatSummaryOf(h.Store().Chat(uri))
	h.DispatchServerAction(sessionChannel, ahptypes.StateAction{Value: &ahptypes.SessionChatAddedAction{Type: ahptypes.ActionTypeSessionChatAdded, Summary: summary}})
	h.DispatchServerAction(sessionChannel, ahptypes.StateAction{Value: &ahptypes.SessionDefaultChatChangedAction{Type: ahptypes.ActionTypeSessionDefaultChatChanged, DefaultChat: &uri}})
	return uri
}

func jsonEqual(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

func equalSummary(a, b ahptypes.ChatSummary) bool {
	return a.Resource == b.Resource && a.Title == b.Title && a.Status == b.Status && a.ModifiedAt == b.ModifiedAt &&
		jsonEqual(a.Activity, b.Activity) && jsonEqual(a.Origin, b.Origin) && jsonEqual(a.Interactivity, b.Interactivity) &&
		jsonEqual(a.WorkingDirectories, b.WorkingDirectories)
}

// SyncChatSummary mirrors a chat's denormalised summary fields into its owning session.
//
// Partial updates cannot express removal of an optional JSON field: a nil value disappears on
// the wire and means "unchanged". The action's full-summary upsert form is used when one of those
// fields is cleared. It reports whether anything was dispatched.
func SyncChatSummary(h *host.Host, sessionChannel, chatChannel string) bool {
	chat := h.Store().Chat(chatChannel)
	session := h.Store().Session(sessionChannel)
	if chat == nil || session == nil {
		return false
	}
	var previous *ahptypes.ChatSummary
	for i := range session.Chats {
		if session.Chats[i].Resource == chatChannel {
			previous = &session.Chats[i]
			break
		}
	}
	if previous == nil {
		return false
	}
	current := ChatSummaryOf(chat)
	if equalSummary(*previous, current) {
		return false
	}
	clears := (previous.Activity != nil && current.Activity == nil) ||
		(previous.Origin != nil && current.Origin == nil) ||
		(previous.Interactivity != nil && current.Interactivity == nil) ||
		(previous.WorkingDirectories != nil && current.WorkingDirectories == nil)
	if clears {
		h.DispatchServerAction(sessionChannel, ahptypes.StateAction{Value: &ahptypes.SessionChatAddedAction{Type: ahptypes.ActionTypeSessionChatAdded, Summary: current}})
		return true
	}
	changes := ahptypes.PartialChatSummary{
		Title: &current.Title, Status: &current.Status, Activity: current.Activity, ModifiedAt: &current.ModifiedAt,
		Origin: current.Origin, WorkingDirectories: current.WorkingDirectories,
	}
	h.DispatchServerAction(sessionChannel, ahptypes.StateAction{Value: &ahptypes.SessionChatUpdatedAction{
		Type: ahptypes.ActionTypeSessionChatUpdated, Chat: chatChannel, Changes: changes,
	}})
	return true
}
