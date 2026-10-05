package pi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/channels"
	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/mapper"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// Provider is the provider id this host advertises and accepts as a session URI alias
// (`pi:/<id>`).
const Provider = "pi"

// Live sessions: creation, disposal, adoption from disk, and routing client actions to whatever
// can carry them out (port of src/pi/session-registry.ts).
//
// The session URI's uuid is Pi's session id, so the two identity spaces are the same one and no
// mapping table is needed.

// LiveSession is everything the host tracks for a live session.
type LiveSession struct {
	URI string
	// SessionID is Pi's session id, identical to the uuid in URI.
	SessionID        string
	WorkingDirectory string
	SessionManager   SessionStore
	CreatedAt        string
	// ChatChannel is the session's single chat channel.
	ChatChannel string

	mu          sync.Mutex
	driver      *ChatDriver
	attaching   chan struct{} // closed when the in-flight backend attach settles
	turnAnchors map[string]string
}

// Driver is the chat driver, nil until a backend is attached (a session read from disk has none).
func (s *LiveSession) Driver() *ChatDriver { s.mu.Lock(); defer s.mu.Unlock(); return s.driver }

// Attaching returns a channel closed when the in-flight backend attach settles, nil if none.
func (s *LiveSession) Attaching() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attaching == nil {
		return nil
	}
	return s.attaching
}

// Store implements [HistorySource].
func (s *LiveSession) Store() SessionStore { return s.SessionManager }

// ID implements [HistorySource].
func (s *LiveSession) ID() string { return s.SessionID }

// Anchor implements [HistorySource].
func (s *LiveSession) Anchor(turnID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.turnAnchors[turnID]
	return a, ok
}

func (s *LiveSession) setAnchor(turnID, entryID string) {
	s.mu.Lock()
	s.turnAnchors[turnID] = entryID
	s.mu.Unlock()
}

// SetAnchors merges turn anchors (from a rebuild of the session file).
func (s *LiveSession) SetAnchors(anchors map[string]string) {
	s.mu.Lock()
	for k, v := range anchors {
		s.turnAnchors[k] = v
	}
	s.mu.Unlock()
}

// BackendFactory creates the agent backend for a session. Absent in storage-only mode.
type BackendFactory func(session *LiveSession) (Backend, error)

// SessionFileDeletionResult is the outcome of deleting a session file.
type SessionFileDeletionResult struct {
	OK    bool
	Error string
}

// LiveCatalogueEntry is a live summary that overrides or supplements Pi's on-disk catalogue.
type LiveCatalogueEntry struct {
	Summary ahptypes.SessionSummary
	File    string
}

// RegistryOptions configure a [Registry].
type RegistryOptions struct {
	Host *host.Host
	// DefaultWorkingDirectory is used for sessions created without one of their own.
	DefaultWorkingDirectory string
	CreateBackend           BackendFactory
	// CreateSessionManager is the explicit storage boundary; composition chooses durable or
	// in-memory sessions.
	CreateSessionManager SessionManagerFactory
	// DefaultSelection seeds a new chat's draft so a client has a model selected from the start.
	DefaultSelection func() *ahptypes.ModelSelection
	// DeleteFile: a failed result or error prevents protocol removal.
	DeleteFile func(path string) (SessionFileDeletionResult, error)
	// FindSessionFile locates the file behind a session this host never ran.
	FindSessionFile func(sessionID string) (string, error)
	// AdoptForeignTurns mirrors turns started outside AHP into the chat (see ChatDriverOptions).
	AdoptForeignTurns bool
	Log               func(format string, args ...any)
}

// Registry owns the live sessions.
type Registry struct {
	opts RegistryOptions
	host *host.Host

	mu        sync.Mutex
	sessions  map[string]*LiveSession
	byChat    map[string]*LiveSession
	baselines map[string]ahptypes.SessionSummary // last root-catalogue value; a diff baseline, never authoritative
	disposals map[string]*disposal
	available []*func(*LiveSession)
	deleted   []*func(string) error
}

type disposal struct {
	done    chan struct{}
	err     error
	waiters int // requests coalesced onto this disposal
}

// NewRegistry creates the registry and hooks it into the host.
func NewRegistry(opts RegistryOptions) *Registry {
	r := &Registry{
		opts: opts, host: opts.Host, sessions: map[string]*LiveSession{}, byChat: map[string]*LiveSession{},
		baselines: map[string]ahptypes.SessionSummary{}, disposals: map[string]*disposal{},
	}
	// Client actions are routed to whichever channel owns them; the host core stays agnostic of
	// chats and backends.
	r.host.OnActionCommitted(r.onCommitted)
	r.host.OnClientAction(r.routeClientAction)
	r.host.AddClientActionValidator(func(channel string, action host.ClientAction, _ string) string {
		return r.validateClientAction(channel, action)
	})
	return r
}

func (r *Registry) logf(format string, args ...any) {
	if r.opts.Log != nil {
		r.opts.Log(format, args...)
	}
}

// ── Lookup ──────────────────────────────────────────────────────────────

// Get returns the live session at a session URI.
func (r *Registry) Get(uri string) (*LiveSession, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[uri]
	return s, ok
}

// GetByChat returns the live session owning a chat channel.
func (r *Registry) GetByChat(chat string) (*LiveSession, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.byChat[chat]
	return s, ok
}

// Has reports whether a session is live.
func (r *Registry) Has(uri string) bool { _, ok := r.Get(uri); return ok }

// Count is the number of live sessions.
func (r *Registry) Count() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.sessions) }

// IsDisposing reports whether a disposal is in flight.
func (r *Registry) IsDisposing(uri string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.disposals[uri]
	return ok
}

// OnSessionAvailable observes sessions after their registry and protocol channels exist,
// including those that already do.
func (r *Registry) OnSessionAvailable(listener func(*LiveSession)) (remove func()) {
	l := &listener
	r.mu.Lock()
	r.available = append(r.available, l)
	existing := make([]*LiveSession, 0, len(r.sessions))
	for _, s := range r.sessions {
		existing = append(existing, s)
	}
	r.mu.Unlock()
	for _, s := range existing {
		r.notifyAvailable(*l, s)
	}
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		for i, x := range r.available {
			if x == l {
				r.available = append(r.available[:i:i], r.available[i+1:]...)
				return
			}
		}
	}
}

// OnSessionDeletionCommitted runs after durable deletion commits, before protocol channels
// disappear.
func (r *Registry) OnSessionDeletionCommitted(listener func(sessionID string) error) (remove func()) {
	l := &listener
	r.mu.Lock()
	r.deleted = append(r.deleted, l)
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		for i, x := range r.deleted {
			if x == l {
				r.deleted = append(r.deleted[:i:i], r.deleted[i+1:]...)
				return
			}
		}
	}
}

// CatalogueOverrides are the live summaries that override or supplement Pi's on-disk catalogue.
func (r *Registry) CatalogueOverrides() []LiveCatalogueEntry {
	r.mu.Lock()
	live := make([]*LiveSession, 0, len(r.sessions))
	for _, s := range r.sessions {
		live = append(live, s)
	}
	r.mu.Unlock()
	out := make([]LiveCatalogueEntry, 0, len(live))
	for _, s := range live {
		summary, err := r.summaryOf(s)
		if err != nil {
			continue
		}
		out = append(out, LiveCatalogueEntry{Summary: summary, File: s.SessionManager.File()})
	}
	return out
}

// ── Lifecycle ───────────────────────────────────────────────────────────

func nowISO() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000Z") }

// Create handles createSession. It returns as soon as the channel exists; readiness arrives later
// as a session/ready action on that channel.
func (r *Registry) Create(_ context.Context, params ahptypes.CreateSessionParams) error {
	uri := params.Channel
	// Only canonical and provider-alias URIs can be recovered after a host restart.
	sessionID, ok := wire.SessionIDFromURI(uri, Provider)
	if !ok {
		return wire.InvalidParams("Not a session URI: " + uri)
	}
	r.mu.Lock()
	_, disposing := r.disposals[uri]
	_, live := r.sessions[uri]
	r.mu.Unlock()
	if disposing || live || r.host.Store().Has(uri) {
		return wire.SessionAlreadyExists(uri)
	}
	if params.Provider != nil && *params.Provider != Provider {
		return wire.ProviderNotFound(*params.Provider)
	}

	workingDirectory := r.opts.DefaultWorkingDirectory
	if len(params.WorkingDirectories) > 0 {
		// A set in the protocol, but this host does not declare multipleWorkingDirectories, which
		// is what forbids a client from sending more than one. Anything past the first is ignored
		// rather than refused: the session still runs, in the directory asked for first.
		path, err := wire.FileURIToPath(params.WorkingDirectories[0])
		if err != nil {
			return wire.InvalidParams(err.Error())
		}
		workingDirectory = path
	}
	if workingDirectory == "" {
		workingDirectory = "."
	}

	title := mapper.NewSessionTitle
	createdAt := nowISO()
	// A provider-alias URI still needs the session reducer.
	if err := r.host.Store().Create(uri, channels.InitialSessionState(Provider, title, workingDirectory), wire.KindSession); err != nil {
		return err
	}
	store, err := r.opts.CreateSessionManager(workingDirectory, sessionID)
	if err != nil {
		r.host.DeleteChannel(uri)
		return fmt.Errorf("could not create the session store: %w", err)
	}
	var selection *ahptypes.ModelSelection
	if r.opts.DefaultSelection != nil {
		selection = r.opts.DefaultSelection()
	}
	chat := channels.InstallDefaultChat(r.host, uri, sessionID, title, selection)

	session := &LiveSession{
		URI: uri, SessionID: sessionID, WorkingDirectory: workingDirectory, SessionManager: store,
		ChatChannel: chat, CreatedAt: createdAt, turnAnchors: map[string]string{},
	}
	r.track(session)
	r.notifyAvailableToAll(session)

	summary, err := r.summaryOf(session)
	if err == nil {
		channels.NotifySessionAdded(r.host, summary)
		r.setBaseline(uri, summary)
	}
	r.bumpActiveSessions()

	// Readiness is genuinely asynchronous once a backend is involved: the client already holds
	// the channel and sees lifecycle "creating" until the agent is up.
	go r.attachBackend(session)
	return nil
}

// AdoptedSession describes a session loaded from disk.
type AdoptedSession struct {
	URI, SessionID, WorkingDirectory, CreatedAt, ChatChannel string
	SessionManager                                           SessionStore
	TurnAnchors                                              map[string]string
}

// Adopt registers a session loaded from disk. Deliberately without a backend: a client browsing
// its history would otherwise start an agent for every session it looks at. The agent is attached
// on the first action that needs one.
func (r *Registry) Adopt(a AdoptedSession) *LiveSession {
	r.mu.Lock()
	if existing, ok := r.sessions[a.URI]; ok {
		r.mu.Unlock()
		return existing
	}
	r.mu.Unlock()
	anchors := a.TurnAnchors
	if anchors == nil {
		anchors = map[string]string{}
	}
	s := &LiveSession{
		URI: a.URI, SessionID: a.SessionID, WorkingDirectory: a.WorkingDirectory, SessionManager: a.SessionManager,
		CreatedAt: a.CreatedAt, ChatChannel: a.ChatChannel, turnAnchors: anchors,
	}
	r.track(s)
	r.notifyAvailableToAll(s)
	if summary, err := r.summaryOf(s); err == nil {
		r.setBaseline(a.URI, summary)
	}
	r.bumpActiveSessions()
	return s
}

// Dispose handles disposeSession.
//
// It works for any session in the catalogue, not just ones this host has running: most sessions a
// client can see were written by Pi and have never been live here, so refusing to dispose them
// would make the delete affordance fail on almost everything the list shows. AHP requires backend
// teardown and catalogue removal but does not prescribe how a host stores sessions; this
// catalogue is the set of Pi session files, so stable removal also requires deleting the backing
// file.
func (r *Registry) Dispose(ctx context.Context, uri string) error {
	r.mu.Lock()
	if pending, ok := r.disposals[uri]; ok {
		pending.waiters++
		r.mu.Unlock()
		<-pending.done
		return pending.err
	}
	d := &disposal{done: make(chan struct{})}
	r.disposals[uri] = d
	r.mu.Unlock()

	d.err = r.disposeOnce(ctx, uri)
	r.mu.Lock()
	if r.disposals[uri] == d {
		delete(r.disposals, uri)
	}
	r.mu.Unlock()
	close(d.done)
	return d.err
}

// DisposalWaiters reports how many requests are coalesced onto the disposal in flight for uri
// (an observation point for tests, which cannot otherwise tell when a second request has arrived).
func (r *Registry) DisposalWaiters(uri string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d, ok := r.disposals[uri]; ok {
		return d.waiters
	}
	return 0
}

func (r *Registry) disposeOnce(ctx context.Context, uri string) error {
	r.mu.Lock()
	session := r.sessions[uri]
	r.mu.Unlock()
	var sessionID, file string
	var quiesced *ChatDriver

	fail := func(err error) error {
		if quiesced != nil {
			quiesced.Resume()
		}
		var pe *wire.Error
		if errors.As(err, &pe) {
			return err
		}
		return fmt.Errorf("Could not dispose session %s: %w", uri, err)
	}

	if session != nil {
		sessionID = session.SessionID
		quiesced = session.Driver()
		if quiesced != nil {
			if err := quiesced.Quiesce(ctx); err != nil {
				return fail(err)
			}
		}
		file = session.SessionManager.File()
		// Storage-only or failed-start backends have no driver to close a turn.
		if chat := r.host.Store().Chat(session.ChatChannel); chat != nil && chat.ActiveTurn != nil {
			duration := int64(0)
			if started, err := time.Parse(time.RFC3339Nano, chat.ActiveTurn.StartedAt); err == nil {
				if d := time.Since(started).Milliseconds(); d > 0 {
					duration = d
				}
			}
			r.host.DispatchServerAction(session.ChatChannel, ahptypes.StateAction{Value: &ahptypes.ChatTurnCancelledAction{
				Type: ahptypes.ActionTypeChatTurnCancelled, TurnId: chat.ActiveTurn.Id, Duration: duration,
			}})
		}
	} else {
		if kind, ok := r.host.Store().KindOf(uri); ok && kind != wire.KindSession {
			return fail(wire.SessionNotFound(uri))
		}
		parsed, ok := wire.SessionIDFromURI(uri, Provider)
		if !ok {
			return fail(wire.SessionNotFound(uri))
		}
		sessionID = parsed
		if r.opts.FindSessionFile != nil {
			f, err := r.opts.FindSessionFile(sessionID)
			if err != nil {
				return fail(err)
			}
			file = f
		}
		if file == "" && !r.host.Store().Has(uri) {
			return fail(wire.SessionNotFound(uri))
		}
	}

	if file != "" {
		if r.opts.DeleteFile == nil {
			return fail(errors.New("durable session deletion is not configured"))
		}
		result, err := r.opts.DeleteFile(file)
		if err != nil {
			return fail(err)
		}
		if !result.OK {
			msg := result.Error
			if msg == "" {
				msg = "the durable session file remains"
			}
			return fail(errors.New(msg))
		}
	}

	// Durable deletion is now committed. Protocol children disappear before the parent so their
	// subscribers can observe final cleanup actions.
	r.mu.Lock()
	listeners := make([]func(string) error, 0, len(r.deleted))
	for _, l := range r.deleted {
		listeners = append(listeners, *l)
	}
	r.mu.Unlock()
	for _, l := range listeners {
		if err := l(sessionID); err != nil {
			r.logf("protocol child cleanup failed for %s: %v", uri, err)
		}
	}

	// Re-read the live entry defensively so cleanup cannot leave registry and protocol state
	// disagreeing.
	r.mu.Lock()
	current := r.sessions[uri]
	if current != nil {
		delete(r.sessions, uri)
		delete(r.byChat, current.ChatChannel)
	}
	delete(r.baselines, uri)
	r.mu.Unlock()
	if current != nil {
		if d := current.Driver(); d != nil {
			d.Dispose()
		}
		// Disposing a session cascades to every chat in its catalogue.
		r.host.DeleteChannel(current.ChatChannel)
	} else {
		r.host.DeleteChannel(wire.ChatURI(sessionID))
	}
	r.host.DeleteChannel(uri)
	channels.NotifySessionRemoved(r.host, uri)
	r.bumpActiveSessions()
	return nil
}

// ── Backend attachment ──────────────────────────────────────────────────

// attachBackend starts the agent for a session. Concurrent actions during startup wait on one
// attempt rather than racing to build a second agent over the same session file.
func (r *Registry) attachBackend(s *LiveSession) {
	s.mu.Lock()
	if s.attaching != nil {
		wait := s.attaching
		s.mu.Unlock()
		<-wait
		return
	}
	done := make(chan struct{})
	s.attaching = done
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.attaching = nil
		s.mu.Unlock()
		close(done)
	}()
	r.startBackend(s)
}

// EnsureBackend attaches the agent to a live session now, instead of on the first client action
// that needs one, and waits for the attempt to settle. It is for a session whose agent is already
// running elsewhere (the PiG session itself) and whose events must be mirrored from the start.
func (r *Registry) EnsureBackend(uri string) error {
	s, ok := r.Get(uri)
	if !ok {
		return fmt.Errorf("session %s is not registered", uri)
	}
	if s.Driver() != nil {
		return nil
	}
	r.attachBackend(s)
	if s.Driver() == nil {
		return fmt.Errorf("the agent for %s could not be attached", uri)
	}
	return nil
}

func (r *Registry) isCurrent(s *LiveSession) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessions[s.URI] == s
}

func (r *Registry) startBackend(s *LiveSession) {
	if r.opts.CreateBackend == nil {
		// Storage-only mode: the session owns identity and history but has no agent, so it is
		// ready as soon as it exists.
		r.host.DispatchServerAction(s.URI, ahptypes.StateAction{Value: &ahptypes.SessionReadyAction{Type: ahptypes.ActionTypeSessionReady}})
		return
	}
	backend, err := r.opts.CreateBackend(s)
	if err == nil {
		// Disposal may win while an asynchronous backend is starting. Never attach a late backend to
		// a session that no longer exists.
		if !r.isCurrent(s) {
			if d, ok := backend.(Disposer); ok {
				if err := d.Dispose(); err != nil {
					r.logf("late backend disposal failed for %s: %v", s.URI, err)
				}
			}
			return
		}
		driver := NewChatDriver(ChatDriverOptions{
			Host: r.host, ChatChannel: s.ChatChannel, Backend: backend, WorkingDirectory: s.WorkingDirectory,
			// A completed turn's last entry is wherever the leaf now points.
			RecordTurnAnchor: func(turnID string) {
				leaf := s.SessionManager.LeafEntry()
				// Navigating to a user entry branches before it, so it cannot represent "keep this
				// cancelled user-only turn" safely.
				if leaf == nil || leaf.Role() == "user" {
					return
				}
				s.setAnchor(turnID, leaf.ID())
			},
			AdoptForeignTurns: r.opts.AdoptForeignTurns,
			Log:               r.opts.Log,
		})
		s.mu.Lock()
		s.driver = driver
		s.mu.Unlock()
		r.host.DispatchServerAction(s.URI, ahptypes.StateAction{Value: &ahptypes.SessionReadyAction{Type: ahptypes.ActionTypeSessionReady}})
		// Tell the client which model and reasoning effort are actually in effect. There is no
		// protocol field for a "default model", but a client initialises its input from the chat's
		// draft, so seeding the draft's selection is how a host answers that question.
		driver.PublishDefaultSelection()
		return
	}
	if !r.isCurrent(s) {
		return
	}
	message := err.Error()
	r.host.DispatchServerAction(s.URI, ahptypes.StateAction{Value: &ahptypes.SessionCreationFailedAction{
		Type: ahptypes.ActionTypeSessionCreationFailed, Error: ahptypes.ErrorInfo{ErrorType: "backendStartFailed", Message: message},
	}})
	// A turn may already be active: the client's chat/turnStarted was reduced before the agent was
	// asked for. Nothing will ever emit agent_settled now, so close it here or the chat sits in
	// progress forever with no reply and no error.
	if chat := r.host.Store().Chat(s.ChatChannel); chat != nil && chat.ActiveTurn != nil {
		r.host.DispatchServerAction(s.ChatChannel, ahptypes.StateAction{Value: &ahptypes.ChatErrorAction{
			Type: ahptypes.ActionTypeChatError, TurnId: chat.ActiveTurn.Id, Duration: 0,
			Part: ahptypes.ErrorResponsePart{Kind: ahptypes.ResponsePartKindError, Error: ahptypes.ErrorInfo{ErrorType: "backendStartFailed", Message: message}},
		}})
	}
}

// ── Routing client actions ──────────────────────────────────────────────

// routeClientAction routes an accepted client action to whatever owns it. Chat actions go to the
// chat's driver, starting the agent first if this session has only ever been read from disk.
// Session actions are handled here: they carry no turn and need no agent.
func (r *Registry) routeClientAction(channel string, action ahptypes.StateAction) {
	r.mu.Lock()
	owning := r.sessions[channel]
	session := r.byChat[channel]
	_, disposing := func() (*disposal, bool) {
		if session == nil {
			return nil, false
		}
		d, ok := r.disposals[session.URI]
		return d, ok
	}()
	r.mu.Unlock()
	if owning != nil {
		r.handleSessionAction(owning, action)
		return
	}
	if session == nil {
		return
	}
	// Truncation waits on the agent and lazy startup waits on the backend: those must not block
	// the caller (a connection's read loop).
	_, truncate := action.Value.(*ahptypes.ChatTruncatedAction)
	if session.Driver() == nil || disposing || truncate {
		go r.routeChat(channel, session, action)
		return
	}
	r.routeChat(channel, session, action)
}

func (r *Registry) routeChat(channel string, session *LiveSession, action ahptypes.StateAction) {
	defer func() {
		if p := recover(); p != nil {
			r.logf("side effect failed for %s: %v", channel, p)
		}
	}()
	if session.Driver() == nil {
		r.attachBackend(session)
	}
	// An action accepted before disposal may have been waiting for lazy backend startup. On
	// failure it still belongs to the surviving session; on success there is no session left to
	// mutate.
	r.mu.Lock()
	pending := r.disposals[session.URI]
	r.mu.Unlock()
	if pending != nil {
		<-pending.done
		if pending.err == nil {
			return
		}
		// Disposal rolled back; continue with the action already in state.
	}
	if !r.isCurrent(session) {
		return
	}
	switch a := action.Value.(type) {
	case *ahptypes.ChatTruncatedAction:
		r.truncate(session, a.TurnId)
		return
	case *ahptypes.ChatTurnStartedAction:
		if chat := r.host.Store().Chat(channel); chat == nil || chat.ActiveTurn == nil || chat.ActiveTurn.Id != a.TurnId {
			return
		}
	}
	if d := session.Driver(); d != nil {
		d.HandleClientAction(channel, action)
	}
}

func (r *Registry) handleSessionAction(session *LiveSession, action ahptypes.StateAction) {
	a, ok := action.Value.(*ahptypes.SessionTitleChangedAction)
	if !ok {
		return
	}
	// The reducer has already updated the in-memory title. Persisting it is what makes the rename
	// survive a restart: Pi stores session names as session_info entries, and this is the same
	// call its own /resume rename makes, so a session renamed here reads the same in Pi's CLI.
	if _, err := session.SessionManager.AppendSessionInfo(a.Title); err != nil {
		r.logf("could not persist title for %s: %v", session.URI, err)
	}
	// The chat mirrors the session's title. Updating its modification time lets the ordinary
	// chat-to-session projection update the root catalogue.
	r.renameChat(session, a.Title, true)
}

func (r *Registry) applyFallbackTitle(s *LiveSession, firstUserMessage string) {
	state := r.host.Store().Session(s.URI)
	if state == nil || state.Title != mapper.NewSessionTitle || s.SessionManager.SessionName() != "" {
		return
	}
	title, ok := mapper.FallbackSessionTitle(firstUserMessage)
	if !ok {
		return
	}
	// This is Pi's unnamed-session fallback, not an explicit session name: publish it to AHP
	// without adding a session_info entry.
	r.host.DispatchServerAction(s.URI, ahptypes.StateAction{Value: &ahptypes.SessionTitleChangedAction{Type: ahptypes.ActionTypeSessionTitleChanged, Title: title}})
	// The host delivers listener callbacks after the current one returns; publish now so the title
	// reaches the root catalogue before the chat projection's status change does.
	r.publishSummaryIfLive(s)
	r.renameChat(s, title, false)
}

func (r *Registry) renameChat(s *LiveSession, title string, touch bool) {
	// There is no chat title action in AHP 0.9, so the authoritative chat state is edited in place
	// (atomically with respect to reduced actions: a read-modify-write here once dropped the
	// actions of a turn already running), then the same projection runs as after ordinary actions.
	changed := r.host.Store().UpdateChat(s.ChatChannel, func(chat *ahptypes.ChatState) bool {
		if chat.Title == title {
			return false
		}
		chat.Title = title
		if touch {
			chat.ModifiedAt = nowISO()
		}
		return true
	})
	if changed {
		r.syncChatProjection(s)
	}
}

// FetchTurns serves fetchTurns; the page is dispatched before this returns.
func (r *Registry) FetchTurns(_ context.Context, params ahptypes.FetchTurnsParams) error {
	session, ok := r.GetByChat(params.Channel)
	if !ok {
		return wire.NotFound(params.Channel)
	}
	return DispatchOlderTurns(r.host, params.Channel, session, params.Cursor)
}

func (r *Registry) truncate(s *LiveSession, turnID *string) {
	anchor, ok := TruncationAnchor(s, turnID)
	label := "(all)"
	if turnID != nil {
		label = *turnID
	}
	if !ok {
		// Validated before the action was applied, so this only happens if the file changed
		// underneath us.
		r.logf("no truncation anchor for %s in %s", label, s.URI)
		return
	}
	if d := s.Driver(); d != nil && !d.Truncate(context.Background(), anchor) {
		// The agent refused (or cannot). Protocol state is already truncated, so the two have
		// diverged; say so loudly rather than pretend.
		r.logf("agent refused truncation of %s; state and session now differ", s.URI)
	}
}

// ── Internals ───────────────────────────────────────────────────────────

func (r *Registry) summaryOf(s *LiveSession) (ahptypes.SessionSummary, error) {
	state := r.host.Store().Session(s.URI)
	if state == nil {
		return ahptypes.SessionSummary{}, fmt.Errorf("Session state is missing: %s", s.URI)
	}
	meta := map[string]json.RawMessage{}
	for k, v := range state.Meta {
		meta[k] = v
	}
	id, _ := json.Marshal(s.SessionID)
	meta["piSessionId"] = id
	return channels.SessionSummaryOf(s.URI, s.CreatedAt, state, meta), nil
}

func (r *Registry) setBaseline(uri string, summary ahptypes.SessionSummary) {
	r.mu.Lock()
	r.baselines[uri] = summary
	r.mu.Unlock()
}

func (r *Registry) publishSummaryIfLive(s *LiveSession) {
	if r.isCurrent(s) {
		r.publishSummary(s)
	}
}

// publishSummary publishes only changed root-catalogue fields.
func (r *Registry) publishSummary(s *LiveSession) {
	current, err := r.summaryOf(s)
	if err != nil {
		return
	}
	r.mu.Lock()
	previous, had := r.baselines[s.URI]
	r.baselines[s.URI] = current
	r.mu.Unlock()
	var changes ahptypes.PartialSessionSummary
	changed := false
	if !had || current.Title != previous.Title {
		changes.Title, changed = &current.Title, true
	}
	if !had || current.Status != previous.Status {
		changes.Status, changed = &current.Status, true
	}
	if !had || current.ModifiedAt != previous.ModifiedAt {
		changes.ModifiedAt, changed = &current.ModifiedAt, true
	}
	if changed {
		channels.NotifySessionSummaryChanged(r.host, s.URI, changes)
	}
}

// onCommitted reacts to every committed action: it keeps the chat's summary mirrored into its
// session and the session's into the root catalogue.
func (r *Registry) onCommitted(channel string, action ahptypes.StateAction) {
	r.mu.Lock()
	owner := r.byChat[channel]
	session := r.sessions[channel]
	r.mu.Unlock()
	if owner != nil {
		if a, ok := action.Value.(*ahptypes.ChatTurnStartedAction); ok {
			r.applyFallbackTitle(owner, a.Message.Text)
		}
		r.syncChatProjection(owner)
		return
	}
	if session != nil {
		r.publishSummary(session)
	}
}

// syncChatProjection projects a reduced chat summary onto its parent session and root catalogue.
//
// The vendored Microsoft 0.9 reducer cannot update top-level SessionState.status, so this deliberately
// leaves that field alone. Clients still receive the live status in SessionState.chats[],
// ChatState, and the root summary.
func (r *Registry) syncChatProjection(s *LiveSession) {
	if !channels.SyncChatSummary(r.host, s.URI, s.ChatChannel) {
		return
	}
	if state := r.host.Store().Session(s.URI); state != nil {
		aggregate := channels.AggregateSessionChats(state)
		if !equalStrPtr(state.Activity, aggregate.Activity) {
			r.host.DispatchServerAction(s.URI, ahptypes.StateAction{Value: &ahptypes.SessionActivityChangedAction{
				Type: ahptypes.ActionTypeSessionActivityChanged, Activity: aggregate.Activity,
			}})
		}
	}
	r.publishSummaryIfLive(s)
}

func equalStrPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func (r *Registry) track(s *LiveSession) {
	r.mu.Lock()
	r.sessions[s.URI] = s
	r.byChat[s.ChatChannel] = s
	r.mu.Unlock()
}

func (r *Registry) notifyAvailableToAll(s *LiveSession) {
	r.mu.Lock()
	ls := make([]func(*LiveSession), 0, len(r.available))
	for _, l := range r.available {
		ls = append(ls, *l)
	}
	r.mu.Unlock()
	for _, l := range ls {
		r.notifyAvailable(l, s)
	}
}

func (r *Registry) notifyAvailable(l func(*LiveSession), s *LiveSession) {
	defer func() {
		if p := recover(); p != nil {
			r.logf("session availability listener failed for %s: %v", s.URI, p)
		}
	}()
	l(s)
}

func (r *Registry) bumpActiveSessions() {
	r.host.DispatchServerAction(wire.RootChannel, ahptypes.StateAction{Value: &ahptypes.RootActiveSessionsChangedAction{
		Type: ahptypes.ActionTypeRootActiveSessionsChanged, ActiveSessions: int64(r.Count()),
	}})
}
