package pi

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/channels"
	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/mapper"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pisession"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// FileFinder locates the file behind a session id ("" when there is none).
type FileFinder interface {
	FindSessionFile(sessionID string) (string, error)
}

// HydratorOptions configure a [Hydrator].
type HydratorOptions struct {
	Host      *host.Host
	Catalogue FileFinder
	IsLive    func(sessionChannel string) bool
	// IsDisposing reports a disposal in flight, which must not be raced by a load.
	IsDisposing       func(sessionChannel string) bool
	FallbackSelection func() *ahptypes.ModelSelection
	// Adopt registers the loaded session with the registry, without a backend.
	Adopt func(AdoptedSession)
	// WrapStore may replace the store a session file was opened into (the running session's
	// store reads the host's live mirror instead of the file); nil keeps the file-backed store.
	WrapStore func(sessionID string, opened SessionStore) SessionStore
	Log       func(format string, args ...any)
}

// Hydrator loads a session that exists only on disk into protocol state, on first subscription
// (port of src/pi/session-hydrator.ts). A chat and its session share one id, so either URI loads
// the pair. The session becomes ready, since its transcript is genuinely available; it simply has
// no agent attached until someone starts a turn.
type Hydrator struct {
	opts HydratorOptions

	mu         sync.Mutex
	hydrations map[string]chan struct{}
}

// NewHydrator creates a hydrator.
func NewHydrator(opts HydratorOptions) *Hydrator {
	return &Hydrator{opts: opts, hydrations: map[string]chan struct{}{}}
}

func (h *Hydrator) logf(format string, args ...any) {
	if h.opts.Log != nil {
		h.opts.Log(format, args...)
	}
}

// Hydrate makes channel exist in the store if a session file backs it; it reports whether the
// channel now exists.
func (h *Hydrator) Hydrate(_ context.Context, channel string) (bool, error) {
	var sessionID string
	var ok bool
	if wire.IsChatChannel(channel) {
		sessionID, ok = wire.ChatIDFromURI(channel)
	} else {
		// The provider alias is explicit: an unknown channel scheme must not become a session
		// merely because its path resembles one.
		sessionID, ok = wire.SessionIDFromURI(channel, Provider)
	}
	if !ok || sessionID == "" {
		return false, nil
	}
	// Register the session under the URI the client actually used, so a non-standard scheme
	// resolves to the same channel it subscribed to.
	session := channel
	if wire.IsChatChannel(channel) {
		session = wire.SessionURI(sessionID)
	}
	if h.opts.IsLive(session) {
		return h.opts.Host.Store().Has(channel), nil
	}
	if h.opts.IsDisposing(session) {
		return false, nil
	}

	h.mu.Lock()
	if pending, ok := h.hydrations[sessionID]; ok {
		h.mu.Unlock()
		<-pending
		return h.opts.Host.Store().Has(channel), nil
	}
	done := make(chan struct{})
	h.hydrations[sessionID] = done
	h.mu.Unlock()
	h.hydrateSession(sessionID, session)
	h.mu.Lock()
	delete(h.hydrations, sessionID)
	h.mu.Unlock()
	close(done)
	return h.opts.Host.Store().Has(channel), nil
}

func firstUserText(turns []ahptypes.Turn) string {
	for _, t := range turns {
		if strings.TrimSpace(t.Message.Text) != "" {
			return t.Message.Text
		}
	}
	return ""
}

func (h *Hydrator) hydrateSession(sessionID, session string) {
	chat := wire.ChatURI(sessionID)
	file, err := h.opts.Catalogue.FindSessionFile(sessionID)
	if err != nil {
		h.logf("cannot search for session %s: %v", sessionID, err)
		return
	}
	if file == "" {
		return
	}
	manager, err := pisession.Open(file)
	if err != nil {
		h.logf("cannot open %s: %v", file, err)
		return
	}

	turns := RebuildHistoryFromSession(manager, RebuildOptions{TurnIDPrefix: sessionID}).Turns
	// Present only when the window really is a tail: its presence is the protocol's signal that
	// more history can be paged in.
	nextCursor := InitialTurnsCursor(manager, sessionID, turns)
	modifiedAt := nowISO()
	if len(turns) > 0 && turns[len(turns)-1].StartedAt != nil {
		modifiedAt = *turns[len(turns)-1].StartedAt
	}
	if info, err := os.Stat(file); err == nil {
		// The file may disappear after it was located; the reconstructed transcript timestamp
		// remains a valid fallback for this snapshot.
		modifiedAt = info.ModTime().UTC().Format("2006-01-02T15:04:05.000Z")
	}
	workingDirectory := manager.Cwd()
	title := mapper.SessionDisplayTitle(manager.SessionName(), firstUserText(turns))

	// Another create/adopt or disposal may have won during file I/O: live state must not be
	// replaced by this disk snapshot.
	if h.opts.IsLive(session) || h.opts.IsDisposing(session) {
		return
	}

	// Read, for the same reason the catalogue reports read; the reducer still clears the bit if a
	// turn starts.
	status := ahptypes.SessionStatusIdle | ahptypes.SessionStatusIsRead

	// Which model this conversation was last using. The agent starts only on the first new turn,
	// so without seeding it here the client's model picker is empty until the user has already
	// sent something. The recorded model is checked field by field: a session file can carry a
	// partially populated entry, and the wire id needs both halves of Pi's (provider, modelId).
	level, provider, modelID := manager.ContextSettings()
	var selection *ahptypes.ModelSelection
	if provider != "" && modelID != "" {
		thinking, _ := json.Marshal(level)
		selection = &ahptypes.ModelSelection{Id: ModelSelectionID(provider, modelID), Config: map[string]json.RawMessage{ThinkingConfigKey: thinking}}
	} else if h.opts.FallbackSelection != nil {
		selection = h.opts.FallbackSelection()
	}

	chatState := &ahptypes.ChatState{Resource: chat, Title: title, Status: status, ModifiedAt: modifiedAt, Turns: turns}
	if nextCursor != "" {
		chatState.TurnsNextCursor = &nextCursor
	}
	if selection != nil {
		chatState.Draft = &ahptypes.Message{Text: "", Origin: ahptypes.MessageOrigin{Kind: ahptypes.MessageKindUser}, Model: selection}
	}
	if err := h.opts.Host.Store().Create(chat, chatState); err != nil {
		h.logf("cannot create %s: %v", chat, err)
		return
	}
	fileMeta, _ := json.Marshal(file)
	sessionState := &ahptypes.SessionState{
		Provider: Provider, Title: title, Status: status,
		// The transcript is genuinely available, so the session is ready.
		Lifecycle: ahptypes.SessionLifecycleReady, ActiveClients: []ahptypes.SessionActiveClient{},
		Chats: []ahptypes.ChatSummary{channels.ChatSummaryOf(chatState)}, DefaultChat: &chat,
		Meta: map[string]json.RawMessage{"piSessionFile": fileMeta, "hydrated": json.RawMessage("true")},
	}
	if workingDirectory != "" {
		sessionState.WorkingDirectories = []ahptypes.URI{wire.PathToFileURI(workingDirectory)}
	}
	if err := h.opts.Host.Store().Create(session, sessionState, wire.KindSession); err != nil {
		h.logf("cannot create %s: %v", session, err)
		return
	}
	createdAt := modifiedAt
	if len(turns) > 0 && turns[0].StartedAt != nil {
		createdAt = *turns[0].StartedAt
	}
	var store SessionStore = manager
	if h.opts.WrapStore != nil {
		store = h.opts.WrapStore(sessionID, store)
	}
	if h.opts.Adopt != nil {
		h.opts.Adopt(AdoptedSession{
			URI: session, SessionID: sessionID, WorkingDirectory: workingDirectory, SessionManager: store,
			ChatChannel: chat, CreatedAt: createdAt,
		})
	}
	h.logf("hydrated %s with %d turns", session, len(turns))
}
